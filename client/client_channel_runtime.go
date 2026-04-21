package client

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2p"
	"github.com/quic-go/quic-go"
	"golang.org/x/net/webdav"
)

type clientChannelRuntime struct {
	readLink           func(net.Conn) (*conn.Link, error)
	ackLink            func(net.Conn, *conn.Link) bool
	handleSpecial      func(*TRPClient, net.Conn, *conn.Link) bool
	authorizeTarget    func(*TRPClient, net.Conn, *conn.Link) bool
	dialTarget         func(*TRPClient, *conn.Link) (net.Conn, error)
	writeConnectResult func(net.Conn, *conn.Link, conn.ConnectResultStatus) bool
	relayTarget        func(net.Conn, net.Conn, *conn.Link)
}

var clientDialContext = func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
	return dialer.DialContext(ctx, network, address)
}

var clientTunnelSendType = SendType

var clientHandleUDP5 = func(ctx context.Context, src net.Conn, timeout time.Duration, localIP string) {
	conn.HandleUdp5(ctx, src, timeout, localIP)
}

var runtimeClientChannel = clientChannelRuntime{
	readLink: readChannelLink,
	ackLink:  ackChannelLink,
	handleSpecial: func(s *TRPClient, src net.Conn, lk *conn.Link) bool {
		return runtimeClientSpecialChannel.Handle(s, src, lk)
	},
	authorizeTarget: func(s *TRPClient, src net.Conn, lk *conn.Link) bool {
		return runtimeClientChannelAuthorization.Authorize(s, src, lk)
	},
	dialTarget: func(s *TRPClient, lk *conn.Link) (net.Conn, error) {
		return runtimeClientChannelDial.Dial(s, lk)
	},
	writeConnectResult: writeChannelConnectResult,
	relayTarget:        relayChannelTarget,
}

var runtimeClientChannelAuthorization = clientChannelAuthorizationRuntime{
	associationID: p2pAssociationIDFromConn,
	policyOf: func(s *TRPClient, associationID string) (p2p.P2PAccessPolicy, bool) {
		return runtimeClientP2PStateRoot.AssociationPolicy(s, associationID)
	},
	allowsTarget: p2pAccessPolicyAllows,
	denyMissing: func(src net.Conn, lk *conn.Link, associationID string) {
		_ = writeChannelConnectResult(src, lk, conn.ConnectResultNotAllowed)
		logs.Warn("deny p2p association=%s target=%s because acl runtime is missing", associationID, lk.Host)
	},
	denyTarget: func(src net.Conn, lk *conn.Link, associationID string) {
		_ = writeChannelConnectResult(src, lk, conn.ConnectResultNotAllowed)
		logs.Warn("deny p2p association=%s target=%s by whitelist policy", associationID, lk.Host)
	},
}

var runtimeClientChannelDial = clientChannelDialRuntime{
	contextOf: func(s *TRPClient) context.Context {
		return s.channelDialContext()
	},
	bindAddr: func(s *TRPClient, lk *conn.Link) net.Addr {
		return s.channelDialBindAddr(lk)
	},
	dial: func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
		return clientDialContext(ctx, dialer, network, address)
	},
}

var runtimeClientSpecialChannel = clientSpecialChannelRuntime{
	handleUDP5: func(s *TRPClient, src net.Conn, lk *conn.Link) bool {
		return s.handleUDP5Channel(src, lk)
	},
	handleFile: func(s *TRPClient, src net.Conn, lk *conn.Link) bool {
		return s.handleFileChannel(src, lk)
	},
}

var runtimeClientChannelDispatch = clientChannelDispatchRuntime{
	launch: func(run func()) {
		if run != nil {
			go run()
		}
	},
}

func (s *TRPClient) openBridgeTunnel() (*conn.Conn, error) {
	bridgeConn, err := s.openBridgeControlConn(s.ctx)
	if err != nil {
		logs.Error("Failed to connect to server %s error: %v", s.svrAddr, err)
		HasFailed = true
		logs.Warn("The connection server failed and will be reconnected in five seconds.")
		return nil, err
	}
	tunnel := bridgeConn.Conn
	if tunnel == nil {
		HasFailed = true
		logs.Error("NewConn returned nil tunnel without error (server=%s tp=%s)", s.svrAddr, s.bridgeConnType)
		return nil, errors.New("nil tunnel")
	}
	if err := clientTunnelSendType(tunnel, common.WORK_CHAN, bridgeConn.UUID); err != nil {
		logs.Error("Failed to send type to server %s error: %v", s.svrAddr, err)
		HasFailed = true
		logs.Warn("The connection server failed and will be reconnected in five seconds.")
		_ = tunnel.Close()
		return nil, err
	}
	return tunnel, nil
}

func (s *TRPClient) installBridgeTunnel(tunnel *conn.Conn) error {
	if tunnel == nil {
		return errors.New("nil tunnel")
	}
	if Ver > 4 && s.bridgeConnType == common.CONN_QUIC {
		qc, ok := tunnel.Conn.(*conn.QuicAutoCloseConn)
		if !ok {
			logs.Error("failed to get quic session")
			_ = tunnel.Close()
			return errors.New("failed to get quic session")
		}
		s.tunnel = qc.GetSession()
		return nil
	}
	s.tunnel = mux.NewMux(tunnel.Conn, s.bridgeConnType, s.disconnectTime, true)
	return nil
}

func (s *TRPClient) acceptTunnelConn() (net.Conn, error) {
	tunnelValue := normalizeClientRuntimeTunnel(s.tunnel)
	if s != nil {
		s.tunnel = tunnelValue
	}
	switch t := tunnelValue.(type) {
	case *mux.Mux:
		return t.Accept()
	case *quic.Conn:
		stream, err := t.AcceptStream(s.ctx)
		if err != nil {
			return nil, err
		}
		return conn.NewQuicStreamConn(stream, t), nil
	default:
		return nil, errors.New("nil tunnel")
	}
}

func (s *TRPClient) serveTunnelAcceptLoop(tunnel *conn.Conn) {
	go func() {
		defer func() { _ = tunnel.Close() }()
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			src, err := s.acceptTunnelConn()
			if err != nil {
				s.noteCloseReason("tunnel accept failed: " + err.Error())
				logs.Warn("Accept error on mux: %v", err)
				s.Close()
				return
			}
			runtimeClientChannelDispatch.Launch(s, src)
		}
	}()
}

func (s *TRPClient) handleChan(src net.Conn) {
	runtimeClientChannelDispatch.Handle(s, src)
}

func (r clientChannelRuntime) Handle(s *TRPClient, src net.Conn) {
	if isNilClientNetConn(src) {
		return
	}
	lk, err := r.readLink(src)
	if err != nil || lk == nil {
		_ = src.Close()
		logs.Error("get connection info from server error %v", err)
		return
	}
	if !r.ackLink(src, lk) {
		return
	}
	if r.handleSpecial(s, src, lk) {
		return
	}
	if lk.Host == "" {
		logs.Trace("new %s connection, remote address:%s", lk.ConnType, lk.RemoteAddr)
		_ = src.Close()
		return
	}
	lk.Host = common.FormatAddress(lk.Host)
	if !r.authorizeTarget(s, src, lk) {
		_ = src.Close()
		return
	}
	targetConn, err := r.dialTarget(s, lk)
	if err == nil && isNilClientNetConn(targetConn) {
		err = net.InvalidAddrError("nil channel target conn")
	}
	if err != nil {
		_ = r.writeConnectResult(src, lk, conn.DialConnectResult(err))
		logs.Warn("connect to %s error %v", lk.Host, err)
		_ = src.Close()
		return
	}
	if !r.writeConnectResult(src, lk, conn.ConnectResultOK) {
		if !isNilClientNetConn(targetConn) {
			_ = targetConn.Close()
		}
		_ = src.Close()
		return
	}
	r.relayTarget(src, targetConn, lk)
}

type clientChannelAuthorizationRuntime struct {
	associationID func(net.Conn) string
	policyOf      func(*TRPClient, string) (p2p.P2PAccessPolicy, bool)
	allowsTarget  func(p2p.P2PAccessPolicy, string) bool
	denyMissing   func(net.Conn, *conn.Link, string)
	denyTarget    func(net.Conn, *conn.Link, string)
}

func (r clientChannelAuthorizationRuntime) Authorize(s *TRPClient, src net.Conn, lk *conn.Link) bool {
	if lk == nil {
		return false
	}
	associationID := r.associationID(src)
	if associationID == "" {
		return true
	}
	policy, ok := r.policyOf(s, associationID)
	if !ok {
		r.denyMissing(src, lk, associationID)
		return false
	}
	if !r.allowsTarget(policy, lk.Host) {
		r.denyTarget(src, lk, associationID)
		return false
	}
	return true
}

type clientChannelDialRuntime struct {
	contextOf func(*TRPClient) context.Context
	bindAddr  func(*TRPClient, *conn.Link) net.Addr
	dial      func(context.Context, *net.Dialer, string, string) (net.Conn, error)
}

func (s *TRPClient) dialChannelTarget(lk *conn.Link) (net.Conn, error) {
	return runtimeClientChannelDial.Dial(s, lk)
}

func (r clientChannelDialRuntime) Dial(s *TRPClient, lk *conn.Link) (net.Conn, error) {
	if lk == nil {
		return nil, net.InvalidAddrError("nil link")
	}
	dialer := &net.Dialer{
		Timeout:   lk.Option.Timeout,
		LocalAddr: r.bindAddr(s, lk),
	}
	return r.dial(r.contextOf(s), dialer, lk.ConnType, lk.Host)
}

func (s *TRPClient) channelDialContext() context.Context {
	if s != nil && s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *TRPClient) channelDialBindAddr(lk *conn.Link) net.Addr {
	if s == nil || lk == nil || !LocalIPForward || s.localIP == "" || !common.IsPublicHost(lk.Host) {
		return nil
	}
	if lk.ConnType == "udp" {
		return common.BuildUDPBindAddr(s.localIP)
	}
	return common.BuildTCPBindAddr(s.localIP)
}

type clientSpecialChannelRuntime struct {
	handleUDP5 func(*TRPClient, net.Conn, *conn.Link) bool
	handleFile func(*TRPClient, net.Conn, *conn.Link) bool
}

func (s *TRPClient) handleSpecialChannel(src net.Conn, lk *conn.Link) bool {
	return runtimeClientSpecialChannel.Handle(s, src, lk)
}

func (r clientSpecialChannelRuntime) Handle(s *TRPClient, src net.Conn, lk *conn.Link) bool {
	if lk == nil {
		return true
	}
	if lk.ConnType == "udp5" {
		return r.handleUDP5(s, src, lk)
	}
	if lk.ConnType == "file" {
		return r.handleFile(s, src, lk)
	}
	return false
}

func (s *TRPClient) handleUDP5Channel(src net.Conn, lk *conn.Link) bool {
	if lk == nil || lk.ConnType != "udp5" {
		return false
	}
	logs.Trace("new %s connection of udp5, remote address:%s", lk.ConnType, lk.RemoteAddr)
	clientHandleUDP5(s.channelDialContext(), src, lk.Option.Timeout, s.udp5ForwardLocalIP())
	return true
}

func (s *TRPClient) handleFileChannel(src net.Conn, lk *conn.Link) bool {
	if lk == nil || lk.ConnType != "file" || s == nil || s.fsm == nil {
		return false
	}
	key := strings.TrimPrefix(strings.TrimSpace(lk.Host), "file://")
	vl, ok := s.fsm.GetListenerByKey(key)
	if !ok {
		logs.Warn("Fail to find file server: %s", key)
		_ = src.Close()
		return true
	}
	rwc := conn.GetConn(src, lk.Crypt, lk.Compress, nil, false, false)
	c := conn.WrapConn(rwc, src)
	vl.ServeVirtual(c)
	return true
}

func (s *TRPClient) udp5ForwardLocalIP() string {
	if s == nil || !LocalIPForward {
		return ""
	}
	return s.localIP
}

type clientChannelDispatchRuntime struct {
	launch func(func())
	handle func(*TRPClient, net.Conn)
}

func (r clientChannelDispatchRuntime) Handle(s *TRPClient, src net.Conn) {
	if s == nil || src == nil {
		return
	}
	if r.handle != nil {
		r.handle(s, src)
		return
	}
	runtimeClientChannel.Handle(s, src)
}

func (r clientChannelDispatchRuntime) Launch(s *TRPClient, src net.Conn) {
	if s == nil || src == nil {
		return
	}
	if r.launch == nil {
		r.Handle(s, src)
		return
	}
	r.launch(func() {
		r.Handle(s, src)
	})
}

func readChannelLink(src net.Conn) (*conn.Link, error) {
	return conn.NewConn(src).GetLinkInfo()
}

func ackChannelLink(src net.Conn, lk *conn.Link) bool {
	if lk == nil || !lk.Option.NeedAck {
		return true
	}
	if err := conn.WriteACK(src, lk.Option.Timeout); err != nil {
		logs.Warn("write ACK failed: %v", err)
		_ = src.Close()
		return false
	}
	logs.Trace("sent ACK before proceeding")
	return true
}

func writeChannelConnectResult(src net.Conn, lk *conn.Link, result conn.ConnectResultStatus) bool {
	if lk == nil || !lk.Option.WaitConnectResult {
		return true
	}
	if err := conn.WriteConnectResult(src, result, lk.Option.Timeout); err != nil {
		logs.Warn("write connect result for %s failed: %v", lk.Host, err)
		return false
	}
	return true
}

func relayChannelTarget(src, targetConn net.Conn, lk *conn.Link) {
	logs.Trace("new %s connection with the goal of %s, remote address:%s", lk.ConnType, lk.Host, lk.RemoteAddr)
	isFramed := lk.ConnType == "udp" && Ver > 6
	conn.CopyWaitGroup(src, targetConn, lk.Crypt, lk.Compress, nil, nil, nil, false, 0, nil, nil, false, isFramed)
}

type FileServerManager struct {
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	wg            sync.WaitGroup
	servers       map[string]*fileServer
	watchStopOnce sync.Once
	watchCh       chan struct{}
}

type fileServer struct {
	srv      *http.Server
	listener *conn.VirtualListener
}

func (fsm *FileServerManager) registerFileServer(key string, entry *fileServer) (*fileServer, bool) {
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if fsm.servers == nil {
		return nil, false
	}
	replaced := fsm.servers[key]
	fsm.servers[key] = entry
	return replaced, true
}

func (fsm *FileServerManager) unregisterCurrentFileServer(key string, entry *fileServer) bool {
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if fsm.servers == nil {
		return false
	}
	current, ok := fsm.servers[key]
	if !ok || current != entry {
		return false
	}
	delete(fsm.servers, key)
	return true
}

func (fsm *FileServerManager) shutdownFileServer(key string, entry *fileServer) {
	if entry == nil {
		return
	}
	if entry.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := entry.srv.Shutdown(ctx); err != nil {
			logs.Error("FileServer Shutdown error [%s]: %v", key, err)
		}
		cancel()
	}
	if entry.listener != nil {
		_ = entry.listener.Close()
	}
}

func NewFileServerManager(parentCtx context.Context) *FileServerManager {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)
	fsm := &FileServerManager{
		ctx:     ctx,
		cancel:  cancel,
		servers: make(map[string]*fileServer),
		watchCh: make(chan struct{}),
	}
	if done := parentCtx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				fsm.CloseAll()
			case <-fsm.watchCh:
			}
		}()
	}
	return fsm
}

func (fsm *FileServerManager) StartFileServer(t *file.Tunnel, vkey string) {
	if fsm.ctx.Err() != nil {
		logs.Warn("file server manager already closed, skip StartFileServer")
		return
	}
	addr := net.JoinHostPort(t.ServerIp, strconv.Itoa(t.Port))
	vl := conn.NewVirtualListener(conn.ParseAddr(addr))
	if t.MultiAccount == nil {
		t.MultiAccount = new(file.MultiAccount)
	}
	ports := common.GetPorts(t.Ports)
	if len(ports) == 0 {
		ports = append(ports, 0)
	}
	t.Port = ports[0]
	key := file.FileTunnelRuntimeKey(vkey, t)
	registered := false
	defer func() {
		if !registered {
			_ = vl.Close()
		}
	}()
	fs := http.FileServer(http.Dir(t.LocalPath))
	davHandler := &webdav.Handler{
		Prefix:     t.StripPre,
		FileSystem: webdav.Dir(t.LocalPath),
		LockSystem: webdav.NewMemLS(),
	}
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET", "HEAD":
			http.StripPrefix(t.StripPre, fs).ServeHTTP(w, r)
		default:
			davHandler.ServeHTTP(w, r)
		}
	})
	accounts := make(map[string]string)
	if t.Client != nil && t.Client.Cnf != nil && t.Client.Cnf.U != "" && t.Client.Cnf.P != "" {
		accounts[t.Client.Cnf.U] = t.Client.Cnf.P
	}
	for user, pass := range file.GetAccountMap(t.MultiAccount) {
		accounts[user] = pass
	}
	for user, pass := range file.GetAccountMap(t.UserAuth) {
		accounts[user] = pass
	}
	if len(accounts) > 0 {
		handler = basicAuth(accounts, "WebDAV", handler)
	}
	if t.ReadOnly {
		handler = readOnly(handler)
	}
	srv := &http.Server{
		BaseContext: func(_ net.Listener) context.Context { return fsm.ctx },
		Handler:     handler,
	}
	logs.Info("start WebDAV server, local path %s, strip prefix %s, remote port %s", t.LocalPath, t.StripPre, t.Ports)
	entry := &fileServer{
		srv:      srv,
		listener: vl,
	}
	replaced, ok := fsm.registerFileServer(key, entry)
	if !ok {
		logs.Warn("file server manager already closed, skip StartFileServer")
		return
	}
	registered = true
	if replaced != nil {
		fsm.shutdownFileServer(key, replaced)
	}

	fsm.wg.Add(1)
	go func() {
		defer fsm.wg.Done()
		if err := srv.Serve(vl); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logs.Error("WebDAV Serve error: %v", err)
		}
		fsm.unregisterCurrentFileServer(key, entry)
	}()
}

func (fsm *FileServerManager) GetListenerByKey(key string) (*conn.VirtualListener, bool) {
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	entry, ok := fsm.servers[key]
	if !ok {
		return nil, false
	}
	return entry.listener, true
}

func (fsm *FileServerManager) CloseAll() {
	if fsm == nil {
		return
	}
	fsm.watchStopOnce.Do(func() {
		if fsm.watchCh != nil {
			close(fsm.watchCh)
		}
	})
	fsm.cancel()
	fsm.mu.Lock()
	entries := fsm.servers
	fsm.servers = nil
	fsm.mu.Unlock()
	for key, e := range entries {
		fsm.shutdownFileServer(key, e)
	}
	fsm.wg.Wait()
}

func basicAuth(users map[string]string, realm string, next http.Handler) http.Handler {
	if len(users) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Basic ") {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		payload, err := base64.StdEncoding.DecodeString(auth[len("Basic "):])
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(string(payload), ":", 2)
		if len(parts) != 2 || users[parts[0]] != parts[1] {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, "PROPFIND":
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, PROPFIND")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})
}
