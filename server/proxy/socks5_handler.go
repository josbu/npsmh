package proxy

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/transport"
)

const (
	ipV4            = 1
	domainName      = 3
	ipV6            = 4
	connectMethod   = 1
	bindMethod      = 2
	associateMethod = 3

	socks5AssociateControlPollInterval = time.Second
)

const (
	succeeded            uint8 = 0
	serverFailure        uint8 = 1
	notAllowed           uint8 = 2
	networkUnreachable   uint8 = 3
	hostUnreachable      uint8 = 4
	connectionRefused    uint8 = 5
	_                    uint8 = 6 // RFC 1928: TTL expired
	commandNotSupported  uint8 = 7
	addrTypeNotSupported uint8 = 8
)

const (
	noAuthMethod     = uint8(0)
	UserPassAuth     = uint8(2)
	noAcceptableAuth = uint8(0xFF)

	userAuthVersion = uint8(1)
	authSuccess     = uint8(0)
	authFailure     = uint8(1)
)

var (
	errSocks5InvalidRequest      = errors.New("socks5: invalid request")
	errSocks5UnsupportedAddrType = errors.New("socks5: unsupported address type")
	errSocks5UnsupportedAuthVer  = errors.New("socks5: unsupported auth version")
)

type socks5Address struct {
	Type byte
	Host string
	Port uint16
}

func (a socks5Address) String() string {
	return net.JoinHostPort(a.Host, strconv.Itoa(int(a.Port)))
}

type socks5Request struct {
	Command     byte
	Destination socks5Address
}

type resolvedSocks5ConnectRequest struct {
	task       *file.Tunnel
	targetAddr string
	replyAddr  socks5Address
}

func (s *TunnelModeServer) handleSocks5Request(c net.Conn) {
	request, err := s.readSocks5CommandRequest(c)
	if err != nil {
		s.rejectSocks5Request(c, err)
		return
	}
	s.dispatchSocks5Request(c, request)
}

func (s *TunnelModeServer) readSocks5CommandRequest(c net.Conn) (*socks5Request, error) {
	request, err := readSocks5Request(c)
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (s *TunnelModeServer) rejectSocks5Request(c net.Conn, err error) {
	replyCode := serverFailure
	if errors.Is(err, errSocks5UnsupportedAddrType) {
		replyCode = addrTypeNotSupported
	}
	logs.Warn("invalid socks5 request from %v: %v", c.RemoteAddr(), err)
	_ = writeSocks5Reply(c, replyCode, socks5ConnReplyAddr(c))
	_ = c.Close()
}

func (s *TunnelModeServer) dispatchSocks5Request(c net.Conn, request *socks5Request) {
	switch request.Command {
	case connectMethod:
		s.handleSocks5Connect(c, request.Destination)
	case bindMethod:
		s.rejectUnsupportedSocks5Command(c)
	case associateMethod:
		s.handleSocks5Associate(c, request.Destination)
	default:
		s.rejectUnsupportedSocks5Command(c)
	}
}

func (s *TunnelModeServer) rejectUnsupportedSocks5Command(c net.Conn) {
	_ = writeSocks5Reply(c, commandNotSupported, socks5ConnReplyAddr(c))
	_ = c.Close()
}

func (s *TunnelModeServer) handleSocks5Connect(c net.Conn, dest socks5Address) {
	request, err := s.resolveSocks5ConnectRequest(c, dest)
	if err != nil {
		s.handleSocks5ConnectResolveError(c, err)
		return
	}

	clientConn := conn.NewConn(c)
	target, link, isLocal, err := s.openSocks5ConnectTarget(clientConn, request.task, request.targetAddr)
	if err != nil || target == nil || link == nil {
		s.handleSocks5ConnectOpenError(c, request, target, link, err)
		return
	}
	if !s.completeSocks5ConnectHandshake(c, request, target, link, isLocal) {
		return
	}
	s.pipeClientConn(target, clientConn, link, request.task.Client, []*file.Flow{request.task.Flow, request.task.Client.Flow}, 0, nil, request.task, isLocal, common.CONN_TCP)
}

func (s *TunnelModeServer) resolveSocks5ConnectTarget(dest socks5Address) string {
	return dest.String()
}

func (s *TunnelModeServer) resolveSocks5ConnectRequest(c net.Conn, dest socks5Address) (resolvedSocks5ConnectRequest, error) {
	task, err := selectValidatedTunnelRuntimeTask(s.CurrentTask())
	if err != nil {
		return resolvedSocks5ConnectRequest{}, err
	}
	targetAddr := s.resolveSocks5ConnectTarget(dest)
	if s.IsClientDestinationAccessDenied(task.Client, task, targetAddr) {
		return resolvedSocks5ConnectRequest{}, errProxyAccessDenied
	}
	return resolvedSocks5ConnectRequest{
		task:       task,
		targetAddr: targetAddr,
		replyAddr:  socks5ConnectReplyAddr(task, c),
	}, nil
}

func (s *TunnelModeServer) handleSocks5ConnectResolveError(c net.Conn, err error) {
	replyCode := serverFailure
	if errors.Is(err, errProxyAccessDenied) {
		replyCode = notAllowed
	} else {
		logs.Warn("reject socks5 connect with malformed runtime task: %v", err)
	}
	writeSocks5ReplyAndClose(c, replyCode)
}

func (s *TunnelModeServer) openSocks5ConnectTarget(clientConn *conn.Conn, task *file.Tunnel, targetAddr string) (net.Conn, *conn.Link, bool, error) {
	return s.openClientLink(
		clientConn,
		task.Client,
		targetAddr,
		common.CONN_TCP,
		task.Target.LocalProxy,
		task,
		conn.WithConnectResult(true),
	)
}

func (s *TunnelModeServer) handleSocks5ConnectOpenError(c net.Conn, request resolvedSocks5ConnectRequest, target net.Conn, link *conn.Link, err error) {
	closeSocks5Conn(target)
	if err != nil {
		logs.Warn("open socks5 connect tunnel failed: client=%d task=%d dest=%s err=%v", request.task.Client.Id, request.task.Id, request.targetAddr, err)
		replyCode := serverFailure
		if errors.Is(err, errProxyAccessDenied) {
			replyCode = notAllowed
		}
		writeSocks5ReplyAndClose(c, replyCode)
	}
	if err == nil && (target == nil || link == nil) {
		closeSocks5Conn(c)
	}
}

func (s *TunnelModeServer) completeSocks5ConnectHandshake(c net.Conn, request resolvedSocks5ConnectRequest, target net.Conn, link *conn.Link, isLocal bool) bool {
	if !isLocal {
		replyCode, waitErr := readSocks5ConnectResult(target, link.Option.Timeout)
		if waitErr != nil {
			logSocks5ConnectRemoteResultTimeout(request.task, request.targetAddr, waitErr)
			logs.Warn("wait socks5 connect result failed: client=%d task=%d dest=%s err=%v", request.task.Client.Id, request.task.Id, request.targetAddr, waitErr)
			closeSocks5Conn(target)
			writeSocks5ReplyAndClose(c, serverFailure)
			return false
		}
		if replyCode != succeeded {
			logSocks5ConnectRemoteFailure(request.task, request.targetAddr, replyCode)
			closeSocks5Conn(target)
			writeSocks5ReplyAndClose(c, replyCode)
			return false
		}
	}
	if err := writeSocks5Reply(c, succeeded, request.replyAddr); err != nil {
		closeSocks5Conn(target)
		closeSocks5Conn(c)
		return false
	}
	return true
}

func writeSocks5ReplyAndClose(c net.Conn, rep uint8) {
	if c == nil {
		return
	}
	_ = writeSocks5Reply(c, rep, socks5ConnReplyAddr(c))
	closeSocks5Conn(c)
}

func closeSocks5Conn(c net.Conn) {
	if c != nil {
		_ = c.Close()
	}
}

func readSocks5ConnectResult(target net.Conn, timeout time.Duration) (uint8, error) {
	status, err := conn.ReadConnectResult(target, timeout)
	if err != nil {
		return serverFailure, err
	}
	switch status {
	case conn.ConnectResultOK:
		return succeeded, nil
	case conn.ConnectResultConnectionRefused:
		return connectionRefused, nil
	case conn.ConnectResultHostUnreachable:
		return hostUnreachable, nil
	case conn.ConnectResultNetworkUnreachable:
		return networkUnreachable, nil
	case conn.ConnectResultNotAllowed:
		return notAllowed, nil
	default:
		return serverFailure, nil
	}
}

func (s *TunnelModeServer) handleSocks5Associate(c net.Conn, dest socks5Address) {
	task := s.CurrentTask()
	if err := validateSocks5UDPTunnelTask(task); err != nil {
		logs.Warn("reject socks5 udp associate with malformed runtime task: %v", err)
		_ = writeSocks5Reply(c, serverFailure, socks5ConnReplyAddr(c))
		_ = c.Close()
		return
	}
	if !s.prepareSocks5Associate(c, task) {
		return
	}
	defer func() { _ = c.Close() }()

	logs.Trace("ASSOCIATE %s", dest.String())
	if s.socks5UDP == nil {
		logs.Error("socks5 udp listener is unavailable on task %d", task.Id)
		_ = writeSocks5Reply(c, serverFailure, socks5ConnReplyAddr(c))
		return
	}

	controlAddr, clientIP, err := resolveSocks5UDPClientIdentity(c)
	if err != nil {
		logs.Warn("resolve socks5 udp associate client identity failed: client=%d task=%d err=%v", task.Client.Id, task.Id, err)
		_ = writeSocks5Reply(c, serverFailure, socks5ConnReplyAddr(c))
		return
	}
	session, err := s.socks5UDP.newSessionWithClientIdentity(c, controlAddr, clientIP)
	if err != nil {
		logs.Warn("open socks5 udp associate tunnel failed: client=%d task=%d err=%v", task.Client.Id, task.Id, err)
		_ = writeSocks5Reply(c, serverFailure, socks5ConnReplyAddr(c))
		return
	}
	defer session.Close()

	if err := writeSocks5Reply(c, succeeded, socks5UDPReplyAddr(task, c, s.socks5UDP.listener, clientIP)); err != nil {
		return
	}
	session.start()

	s.waitForSocks5AssociateControl(c, session)
}

func (s *TunnelModeServer) prepareSocks5Associate(c net.Conn, task *file.Tunnel) bool {
	if tcpConn, ok := c.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(15 * time.Second)
		_ = transport.SetTcpKeepAliveParams(tcpConn, 15, 15, 3)
	}
	if !s.HasActiveDestinationACL(task.Client, task) {
		return true
	}
	logs.Warn("destination acl active, reject socks5 udp associate: client=%d task=%d", task.Client.Id, task.Id)
	_ = writeSocks5Reply(c, notAllowed, socks5ConnReplyAddr(c))
	_ = c.Close()
	return false
}

func (s *TunnelModeServer) waitForSocks5AssociateControl(c net.Conn, session *socks5UDPSession) {
	if c == nil || session == nil {
		return
	}
	if session.doneClosed() {
		session.Wait()
		return
	}

	releaseWake := session.registerCloseWake(func() {
		_ = c.SetReadDeadline(time.Now())
	})
	defer releaseWake()

	var buf [1]byte
	for {
		if session.doneClosed() {
			session.Wait()
			return
		}
		if err := c.SetReadDeadline(time.Now().Add(socks5AssociateControlPollInterval)); err != nil {
			logs.Debug("set socks5 associate control deadline error: %v", err)
		}
		if _, err := c.Read(buf[:]); err != nil {
			if conn.IsTimeout(err) {
				if session.doneClosed() {
					session.Wait()
					return
				}
				continue
			}
			session.Close()
			session.Wait()
			return
		}
	}
}

func readSocks5Request(r io.Reader) (socks5Request, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return socks5Request{}, err
	}
	if header[0] != 5 || header[2] != 0 {
		return socks5Request{}, errSocks5InvalidRequest
	}
	addr, err := readSocks5Address(r, header[3])
	if err != nil {
		return socks5Request{}, err
	}
	return socks5Request{
		Command:     header[1],
		Destination: addr,
	}, nil
}

func readSocks5Address(r io.Reader, atyp byte) (socks5Address, error) {
	host, err := readSocks5Host(r, atyp)
	if err != nil {
		return socks5Address{}, err
	}
	var port uint16
	if err := binary.Read(r, binary.BigEndian, &port); err != nil {
		return socks5Address{}, err
	}
	return socks5Address{Type: atyp, Host: host, Port: port}, nil
}

func readSocks5Host(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case ipV4:
		buf := make(net.IP, net.IPv4len)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return buf.String(), nil
	case ipV6:
		buf := make(net.IP, net.IPv6len)
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", err
		}
		return buf.String(), nil
	case domainName:
		return readSocks5Domain(r)
	default:
		return "", errSocks5UnsupportedAddrType
	}
}

func readSocks5Domain(r io.Reader) (string, error) {
	var length [1]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return "", err
	}
	if length[0] == 0 {
		return "", errSocks5InvalidRequest
	}
	buf := make([]byte, int(length[0]))
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readSocks5Methods(r io.Reader, n int) ([]byte, error) {
	if n < 0 {
		return nil, errSocks5InvalidRequest
	}
	methods := make([]byte, n)
	_, err := io.ReadFull(r, methods)
	return methods, err
}

func readSocks5UserPassCredentials(r io.Reader) (string, string, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return "", "", err
	}
	if header[0] != userAuthVersion {
		return "", "", errSocks5UnsupportedAuthVer
	}
	usernameLen := int(header[1])
	username := make([]byte, usernameLen)
	if _, err := io.ReadFull(r, username); err != nil {
		return "", "", err
	}
	if _, err := io.ReadFull(r, header[:1]); err != nil {
		return "", "", err
	}
	passwordLen := int(header[0])
	password := make([]byte, passwordLen)
	if _, err := io.ReadFull(r, password); err != nil {
		return "", "", err
	}
	return string(username), string(password), nil
}

func socks5ConnReplyAddr(c net.Conn) socks5Address {
	if c == nil {
		return socks5ZeroReplyAddr(false, 0)
	}
	return socks5ReplyAddrFromNetAddr(nil, c.LocalAddr(), nil, 0)
}

func socks5ConnectReplyAddr(task *file.Tunnel, clientConn net.Conn) socks5Address {
	return socks5ReplyAddrFromNetAddr(task, addrOf(clientConn), nil, taskPort(task))
}

func socks5UDPReplyAddr(task *file.Tunnel, tcpConn net.Conn, udpConn net.PacketConn, clientIP net.IP) socks5Address {
	var tcpLocal net.Addr
	if tcpConn != nil {
		tcpLocal = tcpConn.LocalAddr()
	}
	var udpLocal net.Addr
	if udpConn != nil {
		udpLocal = udpConn.LocalAddr()
	}
	fallbackPort := 0
	if task != nil {
		fallbackPort = task.Port
	}
	return socks5ReplyAddrFromNetAddr(task, tcpLocal, udpLocal, fallbackPort, clientIP)
}

func socks5ReplyAddrFromNetAddr(task *file.Tunnel, tcpLocal, udpLocal net.Addr, fallbackPort int, clientIP ...net.IP) socks5Address {
	var client net.IP
	if len(clientIP) > 0 {
		client = common.NormalizeIP(clientIP[0])
	}
	wantV6 := client != nil && client.To4() == nil

	ip := socks5ReplyIP(task, tcpLocal, udpLocal, wantV6)
	port := socks5ReplyPort(udpLocal, tcpLocal, fallbackPort)
	if port <= 0 {
		port = fallbackPort
	}
	if ip == nil {
		return socks5ZeroReplyAddr(wantV6, port)
	}
	if v4 := ip.To4(); v4 != nil {
		return socks5Address{Type: ipV4, Host: v4.String(), Port: uint16(port)}
	}
	return socks5Address{Type: ipV6, Host: ip.To16().String(), Port: uint16(port)}
}

func addrOf(c net.Conn) net.Addr {
	if c == nil {
		return nil
	}
	return c.LocalAddr()
}

func taskPort(task *file.Tunnel) int {
	if task == nil {
		return 0
	}
	return task.Port
}

func socks5ReplyIP(task *file.Tunnel, tcpLocal, udpLocal net.Addr, wantV6 bool) net.IP {
	trySpecificIP := func(value net.IP) net.IP {
		value = common.NormalizeIP(value)
		if value == nil || value.IsUnspecified() || common.IsZeroIP(value) {
			return nil
		}
		if (value.To4() == nil) != wantV6 {
			return nil
		}
		return value
	}

	if task != nil {
		if ip := net.ParseIP(task.ServerIp); ip != nil {
			if specific := trySpecificIP(ip); specific != nil {
				return specific
			}
		}
	}
	for _, addr := range []net.Addr{udpLocal, tcpLocal} {
		switch typed := addr.(type) {
		case *net.UDPAddr:
			if specific := trySpecificIP(typed.IP); specific != nil {
				return specific
			}
		case *net.TCPAddr:
			if specific := trySpecificIP(typed.IP); specific != nil {
				return specific
			}
		}
	}
	return nil
}

func socks5ReplyPort(primary, secondary net.Addr, fallback int) int {
	for _, addr := range []net.Addr{primary, secondary} {
		switch typed := addr.(type) {
		case *net.UDPAddr:
			if typed != nil && typed.Port > 0 {
				return typed.Port
			}
		case *net.TCPAddr:
			if typed != nil && typed.Port > 0 {
				return typed.Port
			}
		}
	}
	return fallback
}

func socks5ZeroReplyAddr(wantV6 bool, port int) socks5Address {
	if wantV6 {
		return socks5Address{Type: ipV6, Host: net.IPv6zero.String(), Port: uint16(port)}
	}
	return socks5Address{Type: ipV4, Host: net.IPv4zero.String(), Port: uint16(port)}
}

func writeSocks5Reply(w io.Writer, rep uint8, addr socks5Address) error {
	reply := make([]byte, 0, 22)
	reply = append(reply, 5, rep, 0, addr.Type)
	switch addr.Type {
	case ipV4:
		ip := net.ParseIP(addr.Host).To4()
		if ip == nil {
			ip = net.IPv4zero.To4()
		}
		reply = append(reply, ip...)
	case ipV6:
		ip := net.ParseIP(addr.Host).To16()
		if ip == nil {
			ip = net.IPv6zero.To16()
		}
		reply = append(reply, ip...)
	case domainName:
		host := addr.Host
		if len(host) > 255 {
			host = host[:255]
		}
		reply = append(reply, byte(len(host)))
		reply = append(reply, host...)
	default:
		reply = append(reply, net.IPv4zero.To4()...)
	}
	reply = binary.BigEndian.AppendUint16(reply, addr.Port)
	_, err := w.Write(reply)
	return err
}
