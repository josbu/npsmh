package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
	"github.com/quic-go/quic-go"
	"golang.org/x/net/proxy"
)

const MaxPad = 64

var Ver = version.GetLatestIndex()

var SkipTLSVerify = false

var DisableP2P = false

var AutoReconnect = true

var P2PMode = common.CONN_QUIC

var LocalIPForward = false

func init() {
	//rand.Seed(time.Now().UnixNano())
	crypt.InitTls(tls.Certificate{})
}

var errAdd = errors.New("the server returned an error, which port or host may have been occupied or not allowed to open")

const maxTaskStatusPayloadSize = 1 << 20

var QuicConfig = &quic.Config{
	KeepAlivePeriod:    10 * time.Second,
	MaxIdleTimeout:     30 * time.Second,
	MaxIncomingStreams: 100000,
}

type clientControlConnPlan struct {
	ctx               context.Context
	tp                string
	server            string
	proxyURL          string
	localIP           string
	verifyCertificate bool
	timeout           time.Duration
	path              string
	alpn              string
	host              string
	sni               string
}

type clientControlTransport struct {
	conn           net.Conn
	isTLS          bool
	tlsVerified    bool
	tlsFingerprint []byte
}

type clientControlVersionPayload struct {
	minVersion []byte
	version    []byte
}

type clientControlRuntimeAuthPayload struct {
	timestamp int64
	info      []byte
	random    []byte
	hmac      []byte
}

type clientConfigSession struct {
	Conn   *conn.Conn
	UUID   string
	VKey   string
	IsPub  bool
	Server string
	Common *config.CommonConfig
}

func normalizeClientParentContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func StartFromFile(pCtx context.Context, pCancel context.CancelFunc, path string) error {
	cnf, err := config.NewConfig(path)
	if err != nil {
		return fmt.Errorf("config file %s loading error %w", path, err)
	}
	return StartFromConfig(pCtx, pCancel, cnf, path)
}

func StartFromConfig(pCtx context.Context, pCancel context.CancelFunc, cnf *config.Config, source string) error {
	pCtx = normalizeClientParentContext(pCtx)
	if cnf == nil || cnf.CommonConfig == nil {
		return fmt.Errorf("config source %s loading error: missing common section", source)
	}
	var uuid string

	first := true
	for {
		select {
		case <-pCtx.Done():
			return nil
		default:
		}
		if !first && (!cnf.CommonConfig.AutoReconnection || !AutoReconnect) {
			if pCancel != nil {
				pCancel()
			}
			return nil
		}
		if !first {
			logs.Info("Reconnecting...")
			if !waitReconnectDelay(pCtx, 5*time.Second) {
				return nil
			}
		}
		first = false

		var err error
		uuid, err = RunConfigOnce(pCtx, pCancel, cnf, source, uuid)
		if err != nil {
			if pCtx.Err() != nil {
				return nil
			}
			logs.Error("Failed to connect: %v", err)
			continue
		}
	}
}

func RunConfigOnce(pCtx context.Context, pCancel context.CancelFunc, cnf *config.Config, source string, uuid string) (string, error) {
	pCtx = normalizeClientParentContext(pCtx)
	if cnf == nil || cnf.CommonConfig == nil {
		return uuid, fmt.Errorf("config source %s loading error: missing common section", source)
	}
	prepareClientConfigRuntime(cnf.CommonConfig, source)
	if len(cnf.LocalServer) > 0 {
		return runClientLocalServerConfig(pCtx, pCancel, cnf.CommonConfig, cnf.LocalServer, uuid)
	}

	session, err := openClientConfigSession(cnf.CommonConfig, uuid)
	if err != nil {
		return uuid, err
	}
	defer session.Close()
	if err := session.publishPublicConfig(); err != nil {
		return session.UUID, err
	}

	ctx, cancel := context.WithCancel(pCtx)
	defer cancel()
	fsm := NewFileServerManager(ctx)
	publishClientConfigHosts(session.Conn, cnf.Hosts)
	publishClientConfigTasks(session.Conn, cnf.Tasks, fsm, session.VKey)
	logClientConfigWebAccess(resolveClientConfigWebAccess(session.Common, session.VKey))

	NewRPClient(session.Server, session.VKey, session.Common.Tp, session.Common.ProxyUrl, session.Common.LocalIP, session.UUID, cnf, session.Common.DisconnectTime, fsm).Start(ctx)
	return session.UUID, nil
}

func prepareClientConfigRuntime(commonCfg *config.CommonConfig, source string) {
	if commonCfg == nil {
		return
	}
	logs.Info("Loading configuration %s successfully", source)
	logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, version.GetLatest())

	common.SetCustomDNS(commonCfg.DnsServer)
	common.SetNtpServer(commonCfg.NtpServer)
	if commonCfg.NtpInterval > 0 {
		common.SetNtpInterval(time.Duration(commonCfg.NtpInterval) * time.Minute)
	}
	common.SyncTime()
	if commonCfg.TlsEnable {
		commonCfg.Tp = "tls"
	}
}

func runClientLocalServerConfig(pCtx context.Context, pCancel context.CancelFunc, commonCfg *config.CommonConfig, localServers []*config.LocalServer, uuid string) (string, error) {
	p2pm := NewP2PManager(pCtx, pCancel, commonCfg)
	for _, local := range localServers {
		current := local
		go func(local *config.LocalServer) { _ = p2pm.StartLocalServer(local) }(current)
	}
	<-pCtx.Done()
	return uuid, pCtx.Err()
}

func openClientConfigSession(commonCfg *config.CommonConfig, uuid string) (*clientConfigSession, error) {
	if commonCfg == nil {
		return nil, fmt.Errorf("missing common config")
	}
	c, assignedUUID, err := NewConn(commonCfg.Tp, commonCfg.VKey, commonCfg.Server, commonCfg.ProxyUrl, commonCfg.LocalIP)
	if err != nil {
		return nil, err
	}
	if uuid == "" {
		uuid = assignedUUID
	}
	if err := SendType(c, common.WORK_CONFIG, uuid); err != nil {
		_ = c.Close()
		return nil, err
	}
	session := &clientConfigSession{
		Conn:   c,
		UUID:   uuid,
		VKey:   commonCfg.VKey,
		Server: commonCfg.Server,
		Common: commonCfg,
	}
	if err := binary.Read(c, binary.LittleEndian, &session.IsPub); err != nil {
		_ = c.Close()
		return nil, err
	}
	return session, nil
}

func (s *clientConfigSession) Close() error {
	if s == nil || s.Conn == nil {
		return nil
	}
	return s.Conn.Close()
}

func (s *clientConfigSession) publishPublicConfig() error {
	if s == nil || s.Conn == nil || !s.IsPub {
		return nil
	}
	if _, err := s.Conn.SendInfo(s.Common.Client, common.NEW_CONF); err != nil {
		return err
	}
	if !s.Conn.GetAddStatus() {
		return fmt.Errorf("the web_user may have been occupied")
	}
	assignedVKey, err := s.Conn.GetShortContent(16)
	if err != nil {
		return err
	}
	s.VKey = string(assignedVKey)
	return nil
}

func publishClientConfigHosts(c *conn.Conn, hosts []*file.Host) {
	if c == nil {
		return
	}
	for _, host := range hosts {
		if _, err := c.SendInfo(host, common.NEW_HOST); err != nil {
			logs.Error("%v", err)
			continue
		}
		if !c.GetAddStatus() {
			logs.Error("%v %s", errAdd, host.Host)
		}
	}
}

func publishClientConfigTasks(c *conn.Conn, tasks []*file.Tunnel, fsm *FileServerManager, vkey string) {
	if c == nil {
		return
	}
	for _, task := range tasks {
		if _, err := c.SendInfo(task, common.NEW_TASK); err != nil {
			logs.Error("%v", err)
			continue
		}
		if !readTaskAddStatuses(c, task) {
			logs.Error("%v %s %s", errAdd, task.Ports, task.Remark)
			continue
		}
		if task.Mode == "file" && fsm != nil {
			go fsm.StartFileServer(task, vkey)
		}
	}
}

func resolveClientConfigWebAccess(commonCfg *config.CommonConfig, vkey string) (string, string) {
	if commonCfg != nil && commonCfg.Client != nil {
		username, password, _ := commonCfg.Client.LegacyWebLoginImport()
		if username != "" && password != "" {
			return username, password
		}
	}
	return "user", vkey
}

func logClientConfigWebAccess(username string, password string) {
	logs.Info("web access login username:%s password:%s", username, password)
}

func waitReconnectDelay(ctx context.Context, delay time.Duration) bool {
	ctx = normalizeClientParentContext(ctx)
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func readTaskAddStatuses(c *conn.Conn, task *file.Tunnel) bool {
	count := taskAddStatusCount(task)
	success := true
	for i := 0; i < count; i++ {
		if !c.GetAddStatus() {
			success = false
		}
	}
	return success
}

func taskAddStatusCount(task *file.Tunnel) int {
	if task == nil {
		return 1
	}
	ports := common.GetPorts(task.Ports)
	if task.Mode == "secret" || task.Mode == "p2p" {
		return 1
	}
	if task.Mode == "file" && len(ports) == 0 {
		return 1
	}
	if len(ports) == 0 {
		return 1
	}
	if len(ports) > 1 && (task.Mode == "tcp" || task.Mode == "udp") {
		targetStr := ""
		if task.Target != nil {
			targetStr = task.Target.TargetStr
		}
		if len(common.GetPorts(targetStr)) != len(ports) {
			return 1
		}
	}
	return len(ports)
}

func GetTaskStatus(server string, vKey string, tp string, proxyUrl string, localIP string) ([]string, error) {
	c, uuid, err := NewConn(tp, vKey, server, proxyUrl, localIP)
	if err != nil {
		return nil, fmt.Errorf("connect status channel: %w", err)
	}
	defer c.Close()
	return getTaskStatus(c, uuid, vKey)
}

func getTaskStatus(c *conn.Conn, uuid string, vKey string) ([]string, error) {
	if err := SendType(c, common.WORK_CONFIG, uuid); err != nil {
		return nil, fmt.Errorf("send status work type: %w", err)
	}
	if _, err := c.BufferWrite([]byte(common.WORK_STATUS)); err != nil {
		return nil, fmt.Errorf("write status opcode: %w", err)
	}
	if _, err := c.Write([]byte(crypt.Blake2b(vKey))); err != nil {
		return nil, fmt.Errorf("write status auth key: %w", err)
	}
	var isPub bool
	if err := binary.Read(c, binary.LittleEndian, &isPub); err != nil {
		return nil, fmt.Errorf("read status publish flag: %w", err)
	}
	data, err := readTaskStatusPayload(c)
	if err != nil {
		return nil, fmt.Errorf("read status payload: %w", err)
	}
	parts := strings.Split(string(data), common.CONN_DATA_SEQ)
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts, nil
}

func readTaskStatusPayload(c *conn.Conn) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("nil status conn")
	}
	length, err := c.GetLen()
	if err != nil {
		return nil, fmt.Errorf("read status payload length: %w", err)
	}
	if length < 0 || length > maxTaskStatusPayloadSize {
		return nil, fmt.Errorf("invalid status payload length %d", length)
	}
	return c.GetShortContent(length)
}

func FormatTaskStatus(parts []string) string {
	var b strings.Builder
	b.WriteString("===== Active Tunnels/Hosts =====\n")
	fmt.Fprintf(&b, "Total active: %d\n", len(parts))
	for i, name := range parts {
		display := name
		if display == "" {
			display = "(no remark)"
		}
		fmt.Fprintf(&b, "  %d. %s\n", i+1, display)
	}
	return b.String()
}

func RegisterLocalIp(server string, vKey string, tp string, proxyUrl string, localIP string, hour int) error {
	c, uuid, err := NewConn(tp, vKey, server, proxyUrl, localIP)
	if err != nil {
		return fmt.Errorf("connect registration channel: %w", err)
	}
	defer c.Close()
	return registerLocalIp(c, uuid, hour)
}

func registerLocalIp(c *conn.Conn, uuid string, hour int) error {
	if err := SendType(c, common.WORK_REGISTER, uuid); err != nil {
		return fmt.Errorf("send register work type: %w", err)
	}
	if err := binary.Write(c, binary.LittleEndian, int32(hour)); err != nil {
		return fmt.Errorf("write registration duration: %w", err)
	}
	return nil
}

func GetProxyConn(proxyUrl, server string, timeout time.Duration, localIP string) (rawConn net.Conn, err error) {
	return GetProxyConnContext(context.Background(), proxyUrl, server, timeout, localIP)
}

func GetProxyConnContext(ctx context.Context, proxyUrl, server string, timeout time.Duration, localIP string) (rawConn net.Conn, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dialer := newClientProxyDialer(timeout, localIP)
	if proxyUrl == "" {
		return dialClientEnvironmentProxy(ctx, dialer, server)
	}
	u, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, err
	}
	return dialClientProxyURL(ctx, u, dialer, server, timeout, localIP)
}

func NewHttpProxyConn(proxyURL *url.URL, remoteAddr string, timeout time.Duration, localIP string) (net.Conn, error) {
	return NewHttpProxyConnContext(context.Background(), proxyURL, remoteAddr, timeout, localIP)
}

func NewHttpProxyConnContext(ctx context.Context, proxyURL *url.URL, remoteAddr string, timeout time.Duration, localIP string) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	proxyConn, err := dialClientHTTPProxy(ctx, proxyURL, timeout, localIP)
	if err != nil {
		return nil, err
	}
	releaseProxyDeadline := applyClientProxyDeadline(proxyConn, timeout)
	defer releaseProxyDeadline()

	req := buildClientHTTPProxyConnectRequest(proxyURL, remoteAddr)
	if err := req.Write(proxyConn); err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	if err := verifyClientHTTPProxyResponse(proxyConn, req); err != nil {
		_ = proxyConn.Close()
		return nil, err
	}
	return proxyConn, nil
}

func newClientProxyDialer(timeout time.Duration, localIP string) *net.Dialer {
	return &net.Dialer{Timeout: timeout, LocalAddr: common.BuildTCPBindAddr(localIP)}
}

func dialClientEnvironmentProxy(ctx context.Context, dialer *net.Dialer, server string) (net.Conn, error) {
	proxyDialer := proxy.FromEnvironmentUsing(dialer)
	if contextDialer, ok := proxyDialer.(proxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, "tcp", server)
	}
	return proxyDialer.Dial("tcp", server)
}

func dialClientProxyURL(ctx context.Context, proxyURL *url.URL, dialer *net.Dialer, server string, timeout time.Duration, localIP string) (net.Conn, error) {
	switch proxyURL.Scheme {
	case "socks5":
		return dialClientSOCKS5Proxy(ctx, proxyURL, dialer, server)
	default:
		return NewHttpProxyConnContext(ctx, proxyURL, server, timeout, localIP)
	}
}

func dialClientSOCKS5Proxy(ctx context.Context, proxyURL *url.URL, dialer *net.Dialer, server string) (net.Conn, error) {
	proxyDialer, err := proxy.FromURL(proxyURL, dialer)
	if err != nil {
		return nil, err
	}
	if contextDialer, ok := proxyDialer.(proxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, "tcp", server)
	}
	return proxyDialer.Dial("tcp", server)
}

func dialClientHTTPProxy(ctx context.Context, proxyURL *url.URL, timeout time.Duration, localIP string) (net.Conn, error) {
	if proxyURL == nil {
		return nil, fmt.Errorf("nil proxy URL")
	}
	dialer := newClientProxyDialer(timeout, localIP)
	return dialer.DialContext(ctx, "tcp", proxyURL.Host)
}

func applyClientProxyDeadline(proxyConn net.Conn, timeout time.Duration) func() {
	if proxyConn == nil {
		return func() {}
	}
	_ = proxyConn.SetDeadline(time.Now().Add(timeout))
	return func() { _ = proxyConn.SetDeadline(time.Time{}) }
}

func buildClientHTTPProxyConnectRequest(proxyURL *url.URL, remoteAddr string) *http.Request {
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: remoteAddr},
		Host:   remoteAddr,
		Header: make(http.Header),
	}
	if proxyURL != nil && proxyURL.User != nil {
		username := proxyURL.User.Username()
		password, _ := proxyURL.User.Password()
		req.SetBasicAuth(username, password)
	}
	return req
}

func verifyClientHTTPProxyResponse(proxyConn net.Conn, req *http.Request) error {
	resp, err := http.ReadResponse(bufio.NewReader(proxyConn), req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errors.New("proxy CONNECT failed: " + resp.Status)
	}
	return nil
}

func newClientTLSConfig(serverName string, verifyCertificate bool, nextProtos ...string) *tls.Config {
	cfg := &tls.Config{
		InsecureSkipVerify: !verifyCertificate,
		ServerName:         common.RemovePortFromHost(serverName),
	}
	if len(nextProtos) > 0 {
		cfg.NextProtos = append([]string(nil), nextProtos...)
	}
	return cfg
}

func newClientP2PTLSConfig(verifyCertificate bool) *tls.Config {
	return newClientTLSConfig(crypt.GetFakeDomainName(), verifyCertificate, "h3")
}

func VerifyState(state tls.ConnectionState, host string) (fingerprint []byte, verified bool) {
	if len(state.PeerCertificates) == 0 {
		return nil, false
	}
	leaf := state.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		inter.AddCert(cert)
	}
	roots, _ := x509.SystemCertPool()
	opts := x509.VerifyOptions{
		DNSName:       host,
		Roots:         roots,
		Intermediates: inter,
	}
	if _, err := leaf.Verify(opts); err != nil {
		verified = false
	} else {
		verified = true
	}
	sum := sha256.Sum256(leaf.Raw)
	return sum[:], verified
}

func VerifyTLS(connection net.Conn, host string) (fingerprint []byte, verified bool) {
	var tlsConn *tls.Conn
	if tc, ok := connection.(*conn.TlsConn); ok {
		tlsConn = tc.Conn
	} else if std, ok := connection.(*tls.Conn); ok {
		tlsConn = std
	} else {
		return nil, false
	}
	if err := tlsConn.Handshake(); err != nil {
		return nil, false
	}
	return VerifyState(tlsConn.ConnectionState(), host)
}

func EnsurePort(server string, tp string) string {
	_, port, err := net.SplitHostPort(server)
	if err == nil && port != "" {
		return server
	}
	if p, ok := common.DefaultPort[tp]; ok {
		return net.JoinHostPort(server, p)
	}
	return server
}

// NewConn Create a new connection with the server and verify it
func NewConn(tp string, vkey string, server string, proxyUrl string, localIP string) (*conn.Conn, string, error) {
	return NewConnWithTLSVerify(tp, vkey, server, proxyUrl, localIP, false)
}

func NewConnWithTLSVerify(tp string, vkey string, server string, proxyUrl string, localIP string, verifyCertificate bool) (*conn.Conn, string, error) {
	return NewConnContextWithTLSVerify(context.Background(), tp, vkey, server, proxyUrl, localIP, verifyCertificate)
}

func NewConnContext(ctx context.Context, tp string, vkey string, server string, proxyUrl string, localIP string) (*conn.Conn, string, error) {
	return NewConnContextWithTLSVerify(ctx, tp, vkey, server, proxyUrl, localIP, false)
}

func NewConnContextWithTLSVerify(ctx context.Context, tp string, vkey string, server string, proxyUrl string, localIP string, verifyCertificate bool) (*conn.Conn, string, error) {
	plan, err := buildClientControlConnPlan(ctx, tp, server, proxyUrl, localIP, verifyCertificate)
	if err != nil {
		return nil, "", err
	}
	transport, err := openClientControlTransport(plan)
	if err != nil {
		return nil, "", err
	}
	c, releaseConn, err := prepareClientControlWireConn(plan, transport.conn)
	if err != nil {
		_ = transport.conn.Close()
		return nil, "", err
	}
	defer releaseConn()

	versionPayload, err := writeClientControlVersion(c)
	if err != nil {
		_ = c.Close()
		return nil, "", err
	}
	uuid, err := authenticateClientControlConn(c, plan, vkey, versionPayload, transport)
	if err != nil {
		_ = c.Close()
		return nil, "", err
	}
	return c, uuid, err
}

func buildClientControlConnPlan(ctx context.Context, tp string, server string, proxyURL string, localIP string, verifyCertificate bool) (clientControlConnPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return clientControlConnPlan{}, err
	}
	timeout := limitClientConnTimeout(ctx, 10*time.Second)
	if timeout <= 0 {
		timeout = time.Millisecond
	}
	path := "/ws"
	alpn := "nps"
	server, path = common.SplitServerAndPath(server)
	if path != "" {
		alpn = strings.TrimSpace(strings.TrimPrefix(path, "/"))
	} else {
		path = "/ws"
	}
	addr, host, sni := common.SplitAddrAndHost(server)
	server = EnsurePort(addr, tp)
	if HasFailed {
		if fastServer, err := common.GetFastAddr(server, tp); err == nil {
			server = fastServer
		} else {
			logs.Debug("Server: %s Path: %s Error: %v", server, path, err)
		}
	}
	return clientControlConnPlan{
		ctx:               ctx,
		tp:                tp,
		server:            server,
		proxyURL:          proxyURL,
		localIP:           localIP,
		verifyCertificate: verifyCertificate,
		timeout:           timeout,
		path:              path,
		alpn:              alpn,
		host:              host,
		sni:               sni,
	}, nil
}

func openClientControlTransport(plan clientControlConnPlan) (clientControlTransport, error) {
	switch plan.tp {
	case "tcp":
		return openClientTCPControlTransport(plan)
	case "tls":
		return openClientTLSControlTransport(plan)
	case "ws":
		return openClientWSControlTransport(plan)
	case "wss":
		return openClientWSSControlTransport(plan)
	case "quic":
		return openClientQUICControlTransport(plan)
	default:
		return openClientKCPControlTransport(plan)
	}
}

func openClientTCPControlTransport(plan clientControlConnPlan) (clientControlTransport, error) {
	rawConn, err := GetProxyConnContext(plan.ctx, plan.proxyURL, plan.server, plan.timeout, plan.localIP)
	if err != nil {
		return clientControlTransport{}, err
	}
	return clientControlTransport{conn: rawConn}, nil
}

func openClientTLSControlTransport(plan clientControlConnPlan) (clientControlTransport, error) {
	rawConn, err := GetProxyConnContext(plan.ctx, plan.proxyURL, plan.server, plan.timeout, plan.localIP)
	if err != nil {
		return clientControlTransport{}, err
	}
	conf := newClientTLSConfig(plan.sni, plan.verifyCertificate)
	tlsConn, err := conn.NewTlsConnContext(plan.ctx, rawConn, plan.timeout, conf)
	if err != nil {
		_ = rawConn.Close()
		return clientControlTransport{}, err
	}
	fingerprint, verified := VerifyTLS(tlsConn, plan.sni)
	return clientControlTransport{
		conn:           tlsConn,
		isTLS:          true,
		tlsVerified:    verified,
		tlsFingerprint: fingerprint,
	}, nil
}

func openClientWSControlTransport(plan clientControlConnPlan) (clientControlTransport, error) {
	rawConn, err := GetProxyConnContext(plan.ctx, plan.proxyURL, plan.server, plan.timeout, plan.localIP)
	if err != nil {
		return clientControlTransport{}, err
	}
	urlStr := "ws://" + plan.server + plan.path
	wsConn, resp, err := conn.DialWSContext(plan.ctx, rawConn, urlStr, plan.host, plan.timeout)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		_ = rawConn.Close()
		return clientControlTransport{}, err
	}
	return clientControlTransport{conn: conn.NewWsConn(wsConn)}, nil
}

func openClientWSSControlTransport(plan clientControlConnPlan) (clientControlTransport, error) {
	rawConn, err := GetProxyConnContext(plan.ctx, plan.proxyURL, plan.server, plan.timeout, plan.localIP)
	if err != nil {
		return clientControlTransport{}, err
	}
	urlStr := "wss://" + plan.server + plan.path
	wsConn, resp, err := conn.DialWSSContext(plan.ctx, rawConn, urlStr, plan.host, plan.sni, plan.timeout, plan.verifyCertificate)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		_ = rawConn.Close()
		return clientControlTransport{}, err
	}
	transport := clientControlTransport{
		conn:  conn.NewWsConn(wsConn),
		isTLS: true,
	}
	if underlying := wsConn.NetConn(); underlying != nil {
		transport.tlsFingerprint, transport.tlsVerified = VerifyTLS(underlying, plan.sni)
	}
	return transport, nil
}

func openClientQUICControlTransport(plan clientControlConnPlan) (clientControlTransport, error) {
	tlsCfg := newClientTLSConfig(plan.sni, plan.verifyCertificate, plan.alpn)
	dialCtx, cancelDial := context.WithTimeout(plan.ctx, plan.timeout)
	defer cancelDial()

	session, err := conn.DialQuicWithLocalIP(dialCtx, plan.server, tlsCfg, QuicConfig, plan.localIP)
	if err != nil {
		return clientControlTransport{}, fmt.Errorf("quic dial error: %w", err)
	}
	fingerprint, verified := VerifyState(session.ConnectionState().TLS, plan.sni)
	stream, err := session.OpenStreamSync(dialCtx)
	if err != nil {
		_ = session.CloseWithError(0, "")
		return clientControlTransport{}, fmt.Errorf("quic open stream error: %w", err)
	}
	return clientControlTransport{
		conn:           conn.NewQuicAutoCloseConn(stream, session),
		isTLS:          true,
		tlsVerified:    verified,
		tlsFingerprint: fingerprint,
	}, nil
}

func openClientKCPControlTransport(plan clientControlConnPlan) (clientControlTransport, error) {
	session, err := conn.DialKCPWithLocalIP(plan.server, plan.localIP)
	if err != nil {
		return clientControlTransport{}, err
	}
	return clientControlTransport{conn: session}, nil
}

func prepareClientControlWireConn(plan clientControlConnPlan, rawConn net.Conn) (*conn.Conn, func(), error) {
	stopCloseOnCancel := closeConnOnContextDone(plan.ctx, rawConn)
	cleanup := func() {
		stopCloseOnCancel()
		_ = rawConn.SetDeadline(time.Time{})
	}
	_ = rawConn.SetDeadline(time.Now().Add(plan.timeout))
	c := conn.NewConn(rawConn)
	if c == nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("conn.NewConn returned nil (tp=%q server=%q)", plan.tp, plan.server)
	}
	return c, cleanup, nil
}

func writeClientControlVersion(c *conn.Conn) (clientControlVersionPayload, error) {
	if _, err := c.BufferWrite([]byte(common.CONN_TEST)); err != nil {
		return clientControlVersionPayload{}, err
	}
	minVerBytes := []byte(version.GetVersion(Ver))
	if err := c.WriteLenContent(minVerBytes); err != nil {
		return clientControlVersionPayload{}, err
	}
	versionBytes := []byte(version.VERSION)
	padLen := rand.Intn(MaxPad)
	if padLen > 0 {
		versionBytes = append(versionBytes, make([]byte, padLen)...)
	}
	if err := c.WriteLenContent(versionBytes); err != nil {
		return clientControlVersionPayload{}, err
	}
	return clientControlVersionPayload{
		minVersion: minVerBytes,
		version:    versionBytes,
	}, nil
}

func authenticateClientControlConn(c *conn.Conn, plan clientControlConnPlan, vkey string, versionPayload clientControlVersionPayload, transport clientControlTransport) (string, error) {
	if Ver == 0 {
		return "", authenticateLegacyClientControlConn(c, vkey)
	}
	return authenticateRuntimeClientControlConn(c, plan.tp, vkey, versionPayload, transport)
}

func authenticateLegacyClientControlConn(c *conn.Conn, vkey string) error {
	serverVersion, err := c.GetShortContent(32)
	if err != nil {
		logs.Error("%v", err)
		return err
	}
	if crypt.Md5(version.GetVersion(Ver)) != string(serverVersion) {
		logs.Warn("The client does not match the server version. The current core version of the client is %s", version.GetVersion(Ver))
	}
	if _, err := c.BufferWrite([]byte(crypt.Md5(vkey))); err != nil {
		return err
	}
	flag, err := c.ReadFlag()
	if err != nil {
		return err
	}
	if flag == common.VERIFY_EER {
		return fmt.Errorf("validation key %s incorrect", vkey)
	}
	return nil
}

func authenticateRuntimeClientControlConn(c *conn.Conn, tp string, vkey string, versionPayload clientControlVersionPayload, transport clientControlTransport) (string, error) {
	authPayload, err := buildClientControlRuntimeAuthPayload(tp, vkey, versionPayload)
	if err != nil {
		return "", err
	}
	if err := writeClientControlRuntimeAuthPayload(c, vkey, authPayload); err != nil {
		return "", err
	}
	return readClientControlRuntimeAuthResponse(c, vkey, authPayload, transport)
}

func buildClientControlRuntimeAuthPayload(tp string, vkey string, versionPayload clientControlVersionPayload) (clientControlRuntimeAuthPayload, error) {
	infoBuf, err := buildClientControlRuntimeInfo(tp, vkey)
	if err != nil {
		return clientControlRuntimeAuthPayload{}, err
	}
	randBuf, err := common.RandomBytes(1000)
	if err != nil {
		return clientControlRuntimeAuthPayload{}, err
	}
	timestamp := common.TimeNow().Unix() - int64(rand.Intn(6))
	return clientControlRuntimeAuthPayload{
		timestamp: timestamp,
		info:      infoBuf,
		random:    randBuf,
		hmac:      crypt.ComputeHMAC(vkey, timestamp, versionPayload.minVersion, versionPayload.version, infoBuf, randBuf),
	}, nil
}

func buildClientControlRuntimeInfo(tp string, vkey string) ([]byte, error) {
	if Ver < 3 {
		return crypt.EncryptBytes(common.EncodeIP(common.GetOutboundIP()), vkey)
	}
	tpBytes := []byte(tp)
	if len(tpBytes) > 32 {
		return nil, fmt.Errorf("tp too long: %d bytes (max %d)", len(tpBytes), 32)
	}
	buf := make([]byte, 0, len(common.EncodeIP(common.GetOutboundIP()))+1+len(tpBytes))
	buf = append(buf, common.EncodeIP(common.GetOutboundIP())...)
	buf = append(buf, byte(len(tpBytes)))
	buf = append(buf, tpBytes...)
	return crypt.EncryptBytes(buf, vkey)
}

func writeClientControlRuntimeAuthPayload(c *conn.Conn, vkey string, authPayload clientControlRuntimeAuthPayload) error {
	if _, err := c.BufferWrite(common.TimestampToBytes(authPayload.timestamp)); err != nil {
		return err
	}
	if _, err := c.BufferWrite([]byte(crypt.Blake2b(vkey))); err != nil {
		return err
	}
	if err := c.WriteLenContent(authPayload.info); err != nil {
		return err
	}
	if err := c.WriteLenContent(authPayload.random); err != nil {
		return err
	}
	if _, err := c.BufferWrite(authPayload.hmac); err != nil {
		return err
	}
	return nil
}

func readClientControlRuntimeAuthResponse(c *conn.Conn, vkey string, authPayload clientControlRuntimeAuthPayload, transport clientControlTransport) (string, error) {
	serverResponse, err := c.GetShortContent(32)
	if err != nil {
		logs.Error("error reading server response: %v", err)
		return "", fmt.Errorf("validation key %s incorrect", vkey)
	}
	expected := crypt.ComputeHMAC(vkey, authPayload.timestamp, authPayload.hmac, []byte(version.GetVersion(Ver)))
	if !bytes.Equal(serverResponse, expected) {
		logs.Warn("The client does not match the server version. The current core version of the client is %s", version.GetVersion(Ver))
		return "", fmt.Errorf("the client does not match the server version %s", version.GetVersion(Ver))
	}
	if Ver <= 1 {
		return "", nil
	}
	if err := verifyClientControlTransportFingerprint(c, vkey, transport); err != nil {
		return "", err
	}
	if Ver <= 3 {
		return "", nil
	}
	uuid := ""
	if Ver > 5 {
		uuidBuf, err := c.GetShortLenContent()
		if err != nil {
			return "", err
		}
		uuid = string(uuidBuf)
	}
	if _, err := c.GetShortLenContent(); err != nil {
		return "", err
	}
	return uuid, nil
}

func verifyClientControlTransportFingerprint(c *conn.Conn, vkey string, transport clientControlTransport) error {
	fpBuf, err := c.GetShortLenContent()
	if err != nil {
		return err
	}
	fpDec, err := crypt.DecryptBytes(fpBuf, vkey)
	if err != nil {
		return err
	}
	if !SkipTLSVerify && transport.isTLS && !transport.tlsVerified && !bytes.Equal(fpDec, transport.tlsFingerprint) {
		logs.Warn("Application-level certificate fingerprint verification failed. To skip it, please set -skip_verify=true")
		return errors.New("validation cert incorrect")
	}
	crypt.AddTrustedCert(vkey, fpDec)
	return nil
}

func SendType(c *conn.Conn, connType, uuid string) error {
	if c == nil {
		return fmt.Errorf("sendType: nil conn (connType=%s uuid=%s)", connType, uuid)
	}
	if _, err := c.BufferWrite([]byte(connType)); err != nil {
		_ = c.Close()
		return err
	}
	if Ver > 3 {
		// v0.30.0
		if Ver > 5 {
			// v0.32.0
			if err := c.WriteLenContent([]byte(uuid)); err != nil {
				_ = c.Close()
				return err
			}
		}
		randByte, err := common.RandomBytes(1000)
		if err != nil {
			_ = c.Close()
			return err
		}
		if err := c.WriteLenContent(randByte); err != nil {
			_ = c.Close()
			return err
		}
	}
	if err := c.FlushBuf(); err != nil {
		_ = c.Close()
		return err
	}
	c.SetAlive()
	return nil
}

func limitClientConnTimeout(ctx context.Context, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		return fallback
	}
	if ctx == nil {
		return fallback
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return fallback
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Millisecond
	}
	if remaining < fallback {
		return remaining
	}
	return fallback
}

func closeConnOnContextDone(ctx context.Context, connection net.Conn) func() {
	if ctx == nil || connection == nil {
		return func() {}
	}
	doneCtx := ctx.Done()
	if doneCtx == nil {
		return func() {}
	}
	stop := context.AfterFunc(ctx, func() {
		_ = connection.Close()
	})
	return func() {
		if stop != nil {
			_ = stop()
		}
	}
}
