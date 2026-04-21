package conn

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/goroutine"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

const maxLengthPrefixedPayload = int(^uint32(0) >> 1)

var errLengthPrefixedPayloadTooLarge = errors.New("length-prefixed payload exceeds int32 maximum")

func newTLSClientConfig(serverName string, verifyCertificate bool) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: !verifyCertificate,
		ServerName:         common.RemovePortFromHost(serverName),
	}
}

// GetConn get crypt or snappy conn
func GetConn(conn net.Conn, cpt, snappy bool, limiter rate.Limiter, isServer, isLocal bool) io.ReadWriteCloser {
	var wrapped io.ReadWriteCloser
	if !isLocal {
		if cpt {
			if isServer {
				wrapped = crypt.NewTlsServerConn(conn)
			} else {
				wrapped = crypt.NewTlsClientConn(conn)
			}
		} else if snappy {
			wrapped = NewSnappyConn(conn)
		}
	}
	if wrapped == nil {
		wrapped = conn
	}
	if limiter != nil {
		wrapped = rate.NewRateConn(wrapped, limiter)
	}
	return wrapped
}

func WrapNetConnWithLimiter(conn net.Conn, limiter rate.Limiter) net.Conn {
	if conn == nil || limiter == nil {
		return conn
	}
	return wrapConnWithoutParentClose(rate.NewRateConn(conn, limiter), conn)
}

func GetTlsConn(c net.Conn, sni string, verifyCertificate bool) (net.Conn, error) {
	if c == nil {
		return nil, net.ErrClosed
	}
	timeout := normalizeLinkTimeout(0)
	tlsConf := newTLSClientConfig(sni, verifyCertificate)
	tlsConn := tls.Client(c, tlsConf)
	if err := tlsConn.SetDeadline(time.Now().Add(timeout)); err != nil {
		_ = c.Close()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		logs.Error("TLS handshake with backend failed: %v", err)
		_ = tlsConn.Close()
		return nil, err
	}
	if err := tlsConn.SetDeadline(time.Time{}); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// GetLenBytes get the assembled amount data(len 4 and content)
func GetLenBytes(buf []byte) (b []byte, err error) {
	return appendLenBytes(nil, buf)
}

func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	s := strings.ToLower(strings.ReplaceAll(err.Error(), " ", ""))
	return strings.Contains(s, "timeout")
}

func HandleUdp5(ctx context.Context, serverConn net.Conn, timeout time.Duration, localIP string) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Wrap the TCP tunnel with timeout and framed I/O.
	defer func() { _ = serverConn.Close() }()
	timeoutConn := NewTimeoutConn(serverConn, timeout)
	defer func() { _ = timeoutConn.Close() }()
	framed := WrapFramed(timeoutConn)

	// Bind one local UDP socket for all outbound UDP traffic.
	// Using nil lets the kernel pick family/port.
	local, err := net.ListenUDP("udp", common.BuildUDPBindAddr(localIP))
	if err != nil {
		logs.Error("bind local udp port error %v", err)
		return
	}
	defer func() { _ = local.Close() }()
	relayCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Internet -> tunnel: read UDP responses, encode to SOCKS5-UDP, send through tunnel.
	go func() {
		defer cancel()
		buf := common.BufPoolUdp.Get()
		defer common.BufPoolUdp.Put(buf)

		for {
			select {
			case <-relayCtx.Done():
				return
			default:
			}

			n, rAddr, err := local.ReadFromUDP(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					logs.Debug("local UDP closed, exiting goroutine")
					return
				}
				if IsTimeout(err) {
					logs.Debug("temporary UDP read error, retrying: %v", err)
					continue
				}
				logs.Debug("read data from remote server error %v", err)
				return
			}

			// Build a standard SOCKS5-UDP packet: RSV(2)=0, FRAG=0, ATYP+ADDR+PORT + DATA.
			hdr := common.NewUDPHeader(0, 0, common.ToSocksAddr(rAddr))
			dgram := common.NewUDPDatagram(hdr, buf[:n])
			if err := dgram.Write(framed); err != nil {
				logs.Debug("write data to tunnel error %v", err)
				return
			}
		}
	}()

	// Tunnel -> internet: read framed bytes, parse as SOCKS5-UDP, send via local UDP socket.
	frameBuf := common.BufPool.Get()
	defer common.BufPool.Put(frameBuf)

	for {
		select {
		case <-relayCtx.Done():
			return
		default:
		}

		n, err := framed.Read(frameBuf)
		if err != nil {
			logs.Debug("read udp frame from tunnel error %v", err)
			return
		}
		if n < 4 {
			// Too short to contain a valid SOCKS5-UDP header.
			logs.Debug("socks5 udp packet too short: %d", n)
			continue
		}

		// Decode a single SOCKS5-UDP datagram from the frame bytes.
		dgram, err := common.ReadUDPDatagram(bytes.NewReader(frameBuf[:n]))
		if err != nil {
			// Drop malformed or fragmented (FRAG != 0) packets.
			logs.Debug("parse socks5 udp packet error %v", err)
			continue
		}

		// Resolve destination and send the payload out.
		rAddr, err := net.ResolveUDPAddr("udp", dgram.Header.Addr.String())
		if err != nil {
			logs.Debug("resolve dest addr %q error %v", dgram.Header.Addr.String(), err)
			continue
		}
		if _, err := local.WriteTo(dgram.Data, rAddr); err != nil {
			if IsTimeout(err) {
				logs.Debug("temporary UDP write error to %v, retrying: %v", rAddr, err)
				continue
			}
			logs.Debug("write udp to %v error %v", rAddr, err)
			return
		}
	}
}

func WriteACK(c net.Conn, timeout time.Duration) error {
	timeout = normalizeLinkTimeout(timeout)
	_ = c.SetWriteDeadline(time.Now().Add(timeout))
	_, err := c.Write([]byte(common.CONN_ACK))
	_ = c.SetWriteDeadline(time.Time{})
	return err
}

func ReadACK(c net.Conn, timeout time.Duration) error {
	timeout = normalizeLinkTimeout(timeout)
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, len(common.CONN_ACK))
	_, err := io.ReadFull(c, buf)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		return err
	}
	if string(buf) != common.CONN_ACK {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// CopyWaitGroup conn1 mux conn
func CopyWaitGroup(conn1, conn2 net.Conn, crypt bool, snappy bool, conn1Limiter rate.Limiter, conn2Limiter rate.Limiter,
	flows []*file.Flow, isServer bool, proxyProtocol int, rb []byte, task *file.Tunnel, isLocal, isFramed bool) {
	connHandle := GetConn(conn1, crypt, snappy, conn1Limiter, isServer, isLocal)
	if isFramed {
		connHandle = WrapFramed(WrapConn(connHandle, conn1))
	}
	peerConn := WrapNetConnWithLimiter(conn2, conn2Limiter)
	if peerConn == nil {
		peerConn = conn2
	}
	proxyHeader := BuildProxyProtocolHeader(conn2, proxyProtocol)
	if proxyHeader != nil {
		logs.Debug("Sending Proxy Protocol v%d header to backend: %v", proxyProtocol, proxyHeader)
		if _, err := connHandle.Write(proxyHeader); err != nil {
			logs.Warn("failed to write proxy protocol header: %v", err)
			_ = conn1.Close()
			_ = conn2.Close()
			return
		}
	}
	if rb != nil {
		if _, err := connHandle.Write(rb); err != nil {
			logs.Warn("failed to write buffered pre-read data: %v", err)
			_ = conn1.Close()
			_ = conn2.Close()
			return
		}
	}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	err := goroutine.CopyConnsPool.Invoke(goroutine.NewConns(connHandle, peerConn, flows, wg, task))
	if err != nil {
		logs.Error("CopyConnsPool.Invoke failed: %v", err)
		wg.Done()
		_ = conn1.Close()
		_ = conn2.Close()
	}
	wg.Wait()
	_ = conn1.Close()
	_ = conn2.Close()
	//return
}

func ParseAddr(addr string) net.Addr {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return &net.TCPAddr{IP: net.ParseIP(addr), Port: 0}
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ip = net.IPv4zero
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 0
	}
	return &net.TCPAddr{IP: ip, Port: port}
}

func BuildProxyProtocolV1Header(clientAddr, targetAddr net.Addr) []byte {
	meta, ok := buildProxyAddrMeta(clientAddr, targetAddr)
	if !ok {
		return []byte("PROXY UNKNOWN\r\n")
	}

	header := "PROXY " + meta.v1Protocol + " " + meta.clientIP + " " + meta.targetIP + " " +
		strconv.Itoa(int(meta.srcPort)) + " " + strconv.Itoa(int(meta.dstPort)) + "\r\n"
	return []byte(header)
}

func BuildProxyProtocolV2Header(clientAddr, targetAddr net.Addr) []byte {
	const sig = "\r\n\r\n\000\r\nQUIT\n" // 12-byte v2 signature
	meta, ok := buildProxyAddrMeta(clientAddr, targetAddr)
	if !ok {
		header := make([]byte, 16)
		copy(header[:12], sig)
		header[12] = 0x20 // v2 + LOCAL
		return header
	}

	header := make([]byte, 16+meta.addrBytes)
	copy(header[:12], sig)
	header[12] = 0x21 // v2 + PROXY
	header[13] = meta.famProto
	binary.BigEndian.PutUint16(header[14:16], meta.addrBytes)

	if meta.addrBytes == 12 { // IPv4
		copy(header[16:20], meta.srcIP.To4())
		copy(header[20:24], meta.dstIP.To4())
		binary.BigEndian.PutUint16(header[24:26], meta.srcPort)
		binary.BigEndian.PutUint16(header[26:28], meta.dstPort)
	} else { // IPv6
		copy(header[16:32], meta.srcIP.To16())
		copy(header[32:48], meta.dstIP.To16())
		binary.BigEndian.PutUint16(header[48:50], meta.srcPort)
		binary.BigEndian.PutUint16(header[50:52], meta.dstPort)
	}
	return header
}

func BuildProxyProtocolHeader(c net.Conn, proxyProtocol int) []byte {
	if proxyProtocol == 0 {
		return nil
	}
	clientAddr := c.RemoteAddr()
	targetAddr := c.LocalAddr()

	if proxyProtocol == 2 {
		return BuildProxyProtocolV2Header(clientAddr, targetAddr)
	}
	if proxyProtocol == 1 {
		return BuildProxyProtocolV1Header(clientAddr, targetAddr)
	}
	return nil
}

func BuildProxyProtocolHeaderByAddr(clientAddr, targetAddr net.Addr, proxyProtocol int) []byte {
	if proxyProtocol == 0 {
		return nil
	}

	targetAddr = normalizeTarget(clientAddr, targetAddr)

	switch proxyProtocol {
	case 2:
		return BuildProxyProtocolV2Header(clientAddr, targetAddr)
	case 1:
		return BuildProxyProtocolV1Header(clientAddr, targetAddr)
	default:
		return nil
	}
}

func normalizeTarget(src, dst net.Addr) net.Addr {
	switch s := src.(type) {

	// TCP
	case *net.TCPAddr:
		d, _ := dst.(*net.TCPAddr)
		if d == nil {
			d = &net.TCPAddr{Port: 0}
		}
		d.IP = normalizeTargetIP(s.IP, d.IP)
		return d

	// UDP
	case *net.UDPAddr:
		d, _ := dst.(*net.UDPAddr)
		if d == nil {
			d = &net.UDPAddr{Port: 0}
		}
		d.IP = normalizeTargetIP(s.IP, d.IP)
		return d

	// Other
	default:
		return dst
	}
}

func normalizeTargetIP(srcIP, dstIP net.IP) net.IP {
	srcIsV4 := srcIP.To4() != nil
	dstIsV4 := dstIP != nil && dstIP.To4() != nil

	switch {
	case srcIsV4 && !dstIsV4:
		return net.IPv4zero
	case !srcIsV4 && dstIsV4:
		return append(net.IPv6zero[:12], dstIP.To4()...)
	case dstIP == nil || dstIP.IsUnspecified():
		if srcIsV4 {
			return net.IPv4zero
		}
		return net.IPv6zero
	default:
		return dstIP
	}
}

func appendLenBytes(dst []byte, payload []byte) ([]byte, error) {
	if len(payload) > maxLengthPrefixedPayload {
		return nil, errLengthPrefixedPayloadTooLarge
	}
	start := len(dst)
	dst = append(dst, 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(dst[start:start+4], uint32(len(payload)))
	dst = append(dst, payload...)
	return dst, nil
}

type proxyAddrMeta struct {
	v1Protocol string
	famProto   byte
	addrBytes  uint16
	srcIP      net.IP
	dstIP      net.IP
	clientIP   string
	targetIP   string
	srcPort    uint16
	dstPort    uint16
}

func buildProxyAddrMeta(clientAddr, targetAddr net.Addr) (proxyAddrMeta, bool) {
	switch c := clientAddr.(type) {
	case *net.TCPAddr:
		t, ok := targetAddr.(*net.TCPAddr)
		if !ok || c == nil || t == nil {
			return proxyAddrMeta{}, false
		}
		return proxyAddrMetaFromIPs(c.IP, t.IP, uint16(c.Port), uint16(t.Port), true)
	case *net.UDPAddr:
		u, ok := targetAddr.(*net.UDPAddr)
		if !ok || c == nil || u == nil {
			return proxyAddrMeta{}, false
		}
		return proxyAddrMetaFromIPs(c.IP, u.IP, uint16(c.Port), uint16(u.Port), false)
	default:
		return proxyAddrMeta{}, false
	}
}

func proxyAddrMetaFromIPs(srcIP, dstIP net.IP, srcPort, dstPort uint16, tcp bool) (proxyAddrMeta, bool) {
	srcIsV4 := srcIP.To4() != nil
	dstIsV4 := dstIP.To4() != nil
	if srcIsV4 != dstIsV4 {
		return proxyAddrMeta{}, false
	}
	meta := proxyAddrMeta{
		srcIP:    srcIP,
		dstIP:    dstIP,
		clientIP: srcIP.String(),
		targetIP: dstIP.String(),
		srcPort:  srcPort,
		dstPort:  dstPort,
	}
	switch {
	case tcp && srcIsV4:
		meta.v1Protocol = "TCP4"
		meta.famProto = 0x11
		meta.addrBytes = 12
	case tcp && !srcIsV4:
		meta.v1Protocol = "TCP6"
		meta.famProto = 0x21
		meta.addrBytes = 36
	case !tcp && srcIsV4:
		meta.v1Protocol = "TCP4"
		meta.famProto = 0x12
		meta.addrBytes = 12
	default:
		meta.v1Protocol = "TCP6"
		meta.famProto = 0x22
		meta.addrBytes = 36
	}
	return meta, true
}

func GetRealIP(r *http.Request, header string) string {
	if header != "" {
		if v := r.Header.Get(header); v != "" {
			for _, p := range strings.Split(v, ",") {
				if ip := common.GetIpByAddr(strings.TrimSpace(p)); ip != "" {
					return ip
				}
			}
		}
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			return host
		}
		return r.RemoteAddr
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, p := range strings.Split(xff, ",") {
			if ip := common.GetIpByAddr(strings.TrimSpace(p)); ip != "" {
				return ip
			}
		}
	}

	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		for _, p := range strings.Split(xrip, ",") {
			if ip := common.GetIpByAddr(strings.TrimSpace(p)); ip != "" {
				return ip
			}
		}
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func parseTCPAddrMaybe(s string) (*net.TCPAddr, error) {
	if s == "" {
		return nil, nil
	}
	a, err := net.ResolveTCPAddr("tcp", s)
	if err != nil {
		return nil, err
	}
	return a, nil
}
