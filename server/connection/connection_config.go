package connection

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/pmux"
	"github.com/djylb/nps/lib/servercfg"
)

const (
	defaultQUICKeepAliveSec   = 10
	defaultQUICIdleTimeoutSec = 30
	defaultMuxPingIntervalSec = 5
	defaultQUICMaxStreams     = 100000
	defaultQUICALPN           = "nps"
)

var BridgeIp string
var BridgeHost string
var BridgeTcpIp string
var BridgeKcpIp string
var BridgeQuicIp string
var BridgeTlsIp string
var BridgeWsIp string
var BridgeWssIp string
var BridgePort int
var BridgeTcpPort int
var BridgeKcpPort int
var BridgeQuicPort int
var BridgeTlsPort int
var BridgeWsPort int
var BridgeWssPort int
var BridgePath string
var BridgeTrustedIps string
var BridgeRealIpHeader string
var HttpIp string
var HttpPort int
var HttpsPort int
var Http3Port int
var WebHostName string
var WebIp string
var WebPort int
var WebOpenSSL bool
var P2pIp string
var P2pPort int
var QuicAlpn []string
var QuicKeepAliveSec int
var QuicIdleTimeoutSec int
var QuicMaxStreams int64
var MuxPingIntervalSec int

type Config struct {
	BridgeIp           string
	BridgeHost         string
	BridgeTcpIp        string
	BridgeKcpIp        string
	BridgeQuicIp       string
	BridgeTlsIp        string
	BridgeWsIp         string
	BridgeWssIp        string
	BridgePort         int
	BridgeTcpPort      int
	BridgeKcpPort      int
	BridgeQuicPort     int
	BridgeTlsPort      int
	BridgeWsPort       int
	BridgeWssPort      int
	BridgePath         string
	BridgeTrustedIps   string
	BridgeRealIpHeader string
	HttpIp             string
	HttpPort           int
	HttpsPort          int
	Http3Port          int
	WebHost            string
	WebIp              string
	WebPort            int
	WebOpenSSL         bool
	P2pIp              string
	P2pPort            int
	QuicAlpn           []string
	QuicKeepAliveSec   int
	QuicIdleTimeoutSec int
	QuicMaxStreams     int64
	MuxPingIntervalSec int
}

type BridgeRuntimeConfig struct {
	Host         string
	Path         string
	TrustedIPs   string
	RealIPHeader string
	PrimaryPort  int
	TCPIP        string
	TCPPort      int
	KCPIP        string
	KCPPort      int
	QUICIP       string
	QUICPort     int
	TLSIP        string
	TLSPort      int
	WSIP         string
	WSPort       int
	WSSIP        string
	WSSPort      int
}

type QUICRuntimeConfig struct {
	ALPN           []string
	KeepAliveSec   int
	IdleTimeoutSec int
	MaxStreams     int64
}

type HTTPRuntimeConfig struct {
	IP        string
	Port      int
	HTTPSPort int
	HTTP3Port int
}

type WebRuntimeConfig struct {
	Host    string
	IP      string
	Port    int
	OpenSSL bool
}

type P2PRuntimeConfig struct {
	IP   string
	Port int
}

func InitConnectionService() {
	ApplySnapshot(servercfg.Current())
}

func CurrentConfig() Config {
	return Config{
		BridgeIp:           BridgeIp,
		BridgeHost:         BridgeHost,
		BridgeTcpIp:        BridgeTcpIp,
		BridgeKcpIp:        BridgeKcpIp,
		BridgeQuicIp:       BridgeQuicIp,
		BridgeTlsIp:        BridgeTlsIp,
		BridgeWsIp:         BridgeWsIp,
		BridgeWssIp:        BridgeWssIp,
		BridgePort:         BridgePort,
		BridgeTcpPort:      BridgeTcpPort,
		BridgeKcpPort:      BridgeKcpPort,
		BridgeQuicPort:     BridgeQuicPort,
		BridgeTlsPort:      BridgeTlsPort,
		BridgeWsPort:       BridgeWsPort,
		BridgeWssPort:      BridgeWssPort,
		BridgePath:         BridgePath,
		BridgeTrustedIps:   BridgeTrustedIps,
		BridgeRealIpHeader: BridgeRealIpHeader,
		HttpIp:             HttpIp,
		HttpPort:           HttpPort,
		HttpsPort:          HttpsPort,
		Http3Port:          Http3Port,
		WebHost:            WebHostName,
		WebIp:              WebIp,
		WebPort:            WebPort,
		WebOpenSSL:         WebOpenSSL,
		P2pIp:              P2pIp,
		P2pPort:            P2pPort,
		QuicAlpn:           append([]string(nil), QuicAlpn...),
		QuicKeepAliveSec:   QuicKeepAliveSec,
		QuicIdleTimeoutSec: QuicIdleTimeoutSec,
		QuicMaxStreams:     QuicMaxStreams,
		MuxPingIntervalSec: MuxPingIntervalSec,
	}
}

func CurrentBridgeRuntime() BridgeRuntimeConfig {
	return BridgeRuntimeConfig{
		Host:         BridgeHost,
		Path:         BridgePath,
		TrustedIPs:   BridgeTrustedIps,
		RealIPHeader: BridgeRealIpHeader,
		PrimaryPort:  BridgePort,
		TCPIP:        BridgeTcpIp,
		TCPPort:      BridgeTcpPort,
		KCPIP:        BridgeKcpIp,
		KCPPort:      BridgeKcpPort,
		QUICIP:       BridgeQuicIp,
		QUICPort:     BridgeQuicPort,
		TLSIP:        BridgeTlsIp,
		TLSPort:      BridgeTlsPort,
		WSIP:         BridgeWsIp,
		WSPort:       BridgeWsPort,
		WSSIP:        BridgeWssIp,
		WSSPort:      BridgeWssPort,
	}
}

func CurrentQUICRuntime() QUICRuntimeConfig {
	return QUICRuntimeConfig{
		ALPN:           append([]string(nil), QuicAlpn...),
		KeepAliveSec:   QuicKeepAliveSec,
		IdleTimeoutSec: QuicIdleTimeoutSec,
		MaxStreams:     QuicMaxStreams,
	}
}

func CurrentHTTPRuntime() HTTPRuntimeConfig {
	return HTTPRuntimeConfig{
		IP:        HttpIp,
		Port:      HttpPort,
		HTTPSPort: HttpsPort,
		HTTP3Port: Http3Port,
	}
}

func CurrentWebRuntime() WebRuntimeConfig {
	return WebRuntimeConfig{
		Host:    WebHostName,
		IP:      WebIp,
		Port:    WebPort,
		OpenSSL: WebOpenSSL,
	}
}

func CurrentP2PRuntime() P2PRuntimeConfig {
	return P2PRuntimeConfig{
		IP:   P2pIp,
		Port: P2pPort,
	}
}

func ApplySnapshot(snapshot *servercfg.Snapshot) {
	cfg := configFromSnapshot(snapshot)
	applyConnectionConfig(cfg)
	rebuildSharedMuxManager(sharedMuxConfigFromConfig(cfg))
}

func normalizeConnectionPort(port int) int {
	if port == 0 {
		return 0
	}
	if port < 0 || port > 65535 {
		logs.Warn("invalid runtime port %d; ignoring out-of-range listener port", port)
		return 0
	}
	return port
}

func configFromSnapshot(cfg *servercfg.Snapshot) Config {
	cfg = servercfg.Resolve(cfg)
	return normalizeConnectionConfig(Config{
		BridgeIp:           cfg.Network.BridgeIP,
		BridgeHost:         cfg.Network.BridgeHost,
		BridgeTcpIp:        cfg.Network.BridgeTCPIP,
		BridgeKcpIp:        cfg.Network.BridgeKCPIP,
		BridgeQuicIp:       cfg.Network.BridgeQUICIP,
		BridgeTlsIp:        cfg.Network.BridgeTLSIP,
		BridgeWsIp:         cfg.Network.BridgeWSIP,
		BridgeWssIp:        cfg.Network.BridgeWSSIP,
		BridgePort:         cfg.Network.BridgePort,
		BridgeTcpPort:      cfg.Network.BridgeTCPPort,
		BridgeKcpPort:      cfg.Network.BridgeKCPPort,
		BridgeQuicPort:     cfg.Network.BridgeQUICPort,
		BridgeTlsPort:      cfg.Network.BridgeTLSPort,
		BridgeWsPort:       cfg.Network.BridgeWSPort,
		BridgeWssPort:      cfg.Network.BridgeWSSPort,
		BridgePath:         cfg.Network.BridgePath,
		BridgeTrustedIps:   cfg.Network.BridgeTrustedIPs,
		BridgeRealIpHeader: cfg.Network.BridgeRealIPHeader,
		HttpIp:             cfg.Network.HTTPProxyIP,
		HttpPort:           cfg.Network.HTTPProxyPort,
		HttpsPort:          cfg.Network.HTTPSProxyPort,
		Http3Port:          cfg.Network.HTTP3ProxyPort,
		WebHost:            cfg.Web.Host,
		WebIp:              cfg.Network.WebIP,
		WebPort:            cfg.Network.WebPort,
		WebOpenSSL:         cfg.Web.OpenSSL,
		P2pIp:              cfg.Network.P2PIP,
		P2pPort:            cfg.Network.P2PPort,
		QuicAlpn:           append([]string(nil), cfg.Network.QUICALPNList...),
		QuicKeepAliveSec:   cfg.Network.QUICKeepAlivePeriod,
		QuicIdleTimeoutSec: cfg.Network.QUICMaxIdleTimeout,
		QuicMaxStreams:     cfg.Network.QUICMaxIncomingStreams,
		MuxPingIntervalSec: cfg.Network.MuxPingInterval,
	})
}

func normalizeConnectionConfig(cfg Config) Config {
	cfg = normalizeConnectionListenerPorts(cfg)
	cfg = normalizeConnectionTransportConfig(cfg)
	return cfg
}

func normalizeConnectionListenerPorts(cfg Config) Config {
	cfg.BridgePort = normalizeConnectionPort(cfg.BridgePort)
	cfg.BridgeTcpPort = normalizeConnectionPort(cfg.BridgeTcpPort)
	cfg.BridgeKcpPort = normalizeConnectionPort(cfg.BridgeKcpPort)
	cfg.BridgeQuicPort = normalizeConnectionPort(cfg.BridgeQuicPort)
	cfg.BridgeTlsPort = normalizeConnectionPort(cfg.BridgeTlsPort)
	cfg.BridgeWsPort = normalizeConnectionPort(cfg.BridgeWsPort)
	cfg.BridgeWssPort = normalizeConnectionPort(cfg.BridgeWssPort)
	cfg.HttpPort = normalizeConnectionPort(cfg.HttpPort)
	cfg.HttpsPort = normalizeConnectionPort(cfg.HttpsPort)
	cfg.Http3Port = normalizeConnectionPort(cfg.Http3Port)
	cfg.WebPort = normalizeConnectionPort(cfg.WebPort)
	cfg.P2pPort = normalizeConnectionPort(cfg.P2pPort)
	return cfg
}

func normalizeConnectionTransportConfig(cfg Config) Config {
	cfg.QuicAlpn = normalizeQUICALPN(cfg.QuicAlpn)
	cfg.QuicKeepAliveSec = normalizePositiveConnectionInt(cfg.QuicKeepAliveSec, defaultQUICKeepAliveSec)
	cfg.QuicIdleTimeoutSec = normalizePositiveConnectionInt(cfg.QuicIdleTimeoutSec, defaultQUICIdleTimeoutSec)
	cfg.MuxPingIntervalSec = normalizePositiveConnectionInt(cfg.MuxPingIntervalSec, defaultMuxPingIntervalSec)
	cfg.QuicMaxStreams = normalizePositiveConnectionInt64(cfg.QuicMaxStreams, defaultQUICMaxStreams)
	return cfg
}

func normalizeQUICALPN(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return []string{defaultQUICALPN}
	}
	return normalized
}

func normalizePositiveConnectionInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func normalizePositiveConnectionInt64(value, fallback int64) int64 {
	if value <= 0 {
		return fallback
	}
	return value
}

func (cfg Config) Snapshot() *servercfg.Snapshot {
	return &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Host:    cfg.WebHost,
			OpenSSL: cfg.WebOpenSSL,
		},
		Network: servercfg.NetworkConfig{
			BridgeIP:               cfg.BridgeIp,
			BridgeHost:             cfg.BridgeHost,
			BridgeTCPIP:            cfg.BridgeTcpIp,
			BridgeKCPIP:            cfg.BridgeKcpIp,
			BridgeQUICIP:           cfg.BridgeQuicIp,
			BridgeTLSIP:            cfg.BridgeTlsIp,
			BridgeWSIP:             cfg.BridgeWsIp,
			BridgeWSSIP:            cfg.BridgeWssIp,
			BridgePort:             cfg.BridgePort,
			BridgeTCPPort:          cfg.BridgeTcpPort,
			BridgeKCPPort:          cfg.BridgeKcpPort,
			BridgeQUICPort:         cfg.BridgeQuicPort,
			BridgeTLSPort:          cfg.BridgeTlsPort,
			BridgeWSPort:           cfg.BridgeWsPort,
			BridgeWSSPort:          cfg.BridgeWssPort,
			BridgePath:             cfg.BridgePath,
			BridgeTrustedIPs:       cfg.BridgeTrustedIps,
			BridgeRealIPHeader:     cfg.BridgeRealIpHeader,
			HTTPProxyIP:            cfg.HttpIp,
			HTTPProxyPort:          cfg.HttpPort,
			HTTPSProxyPort:         cfg.HttpsPort,
			HTTP3ProxyPort:         cfg.Http3Port,
			WebIP:                  cfg.WebIp,
			WebPort:                cfg.WebPort,
			P2PIP:                  cfg.P2pIp,
			P2PPort:                cfg.P2pPort,
			QUICALPNList:           append([]string(nil), cfg.QuicAlpn...),
			QUICKeepAlivePeriod:    cfg.QuicKeepAliveSec,
			QUICMaxIdleTimeout:     cfg.QuicIdleTimeoutSec,
			QUICMaxIncomingStreams: cfg.QuicMaxStreams,
			MuxPingInterval:        cfg.MuxPingIntervalSec,
		},
	}
}

func applyConnectionConfig(cfg Config) {
	applyBridgeConnectionConfig(cfg)
	applyHTTPConnectionConfig(cfg)
	applyWebConnectionConfig(cfg)
	applyP2PConnectionConfig(cfg)
	applyTransportConnectionConfig(cfg)
}

func applyBridgeConnectionConfig(cfg Config) {
	BridgeIp = cfg.BridgeIp
	BridgeHost = cfg.BridgeHost
	BridgeTcpIp = cfg.BridgeTcpIp
	BridgeKcpIp = cfg.BridgeKcpIp
	BridgeQuicIp = cfg.BridgeQuicIp
	BridgeTlsIp = cfg.BridgeTlsIp
	BridgeWsIp = cfg.BridgeWsIp
	BridgeWssIp = cfg.BridgeWssIp
	BridgePort = cfg.BridgePort
	BridgeTcpPort = cfg.BridgeTcpPort
	BridgeKcpPort = cfg.BridgeKcpPort
	BridgeQuicPort = cfg.BridgeQuicPort
	BridgeTlsPort = cfg.BridgeTlsPort
	BridgeWsPort = cfg.BridgeWsPort
	BridgeWssPort = cfg.BridgeWssPort
	BridgePath = cfg.BridgePath
	BridgeTrustedIps = cfg.BridgeTrustedIps
	BridgeRealIpHeader = cfg.BridgeRealIpHeader
}

func applyHTTPConnectionConfig(cfg Config) {
	HttpIp = cfg.HttpIp
	HttpPort = cfg.HttpPort
	HttpsPort = cfg.HttpsPort
	Http3Port = cfg.Http3Port
}

func applyWebConnectionConfig(cfg Config) {
	WebHostName = cfg.WebHost
	WebIp = cfg.WebIp
	WebPort = cfg.WebPort
	WebOpenSSL = cfg.WebOpenSSL
}

func applyP2PConnectionConfig(cfg Config) {
	P2pIp = cfg.P2pIp
	P2pPort = cfg.P2pPort
}

func applyTransportConnectionConfig(cfg Config) {
	QuicAlpn = append([]string(nil), cfg.QuicAlpn...)
	QuicKeepAliveSec = cfg.QuicKeepAliveSec
	QuicIdleTimeoutSec = cfg.QuicIdleTimeoutSec
	QuicMaxStreams = cfg.QuicMaxStreams
	MuxPingIntervalSec = cfg.MuxPingIntervalSec
	mux.PingInterval = time.Duration(MuxPingIntervalSec) * time.Second
}

func GetBridgeTcpListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is tcp, the bridge port is %d", BridgeTcpPort)
	if err := validateTCPPort(BridgeTcpPort, "tcp bridge"); err != nil {
		return nil, err
	}
	if mux, err := getSharedPortMux(routeBridgeTCP); err != nil {
		return nil, err
	} else if mux != nil {
		return mux.GetClientListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeTcpIp), Port: BridgeTcpPort})
}

func GetBridgeTlsListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is tls, the bridge port is %d", BridgeTlsPort)
	if err := validateTCPPort(BridgeTlsPort, "tls bridge"); err != nil {
		return nil, err
	}
	if mux, err := getSharedPortMux(routeBridgeTLS); err != nil {
		return nil, err
	} else if mux != nil {
		return mux.GetClientTlsListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeTlsIp), Port: BridgeTlsPort})
}

func GetBridgeWsListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is ws, the bridge port is %d, the bridge path is %s", BridgeWsPort, BridgePath)
	if err := validateTCPPort(BridgeWsPort, "ws bridge"); err != nil {
		return nil, err
	}
	if mux, err := getSharedPortMux(routeBridgeWS); err != nil {
		return nil, err
	} else if mux != nil {
		return mux.GetClientWsListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeWsIp), Port: BridgeWsPort})
}

func GetBridgeWssListener() (net.Listener, error) {
	logs.Info("server start, the bridge type is wss, the bridge port is %d, the bridge path is %s", BridgeWssPort, BridgePath)
	if err := validateTCPPort(BridgeWssPort, "wss bridge"); err != nil {
		return nil, err
	}
	if mux, err := getSharedPortMux(routeBridgeWSS); err != nil {
		return nil, err
	} else if mux != nil {
		return mux.GetClientWssListener(), nil
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(BridgeWssIp), Port: BridgeWssPort})
}

func GetBridgeReservedTLSListener() (net.Listener, error) {
	if BridgeTlsPort <= 0 || BridgeWssPort <= 0 {
		return nil, nil
	}
	tlsMux, err := getSharedPortMux(routeBridgeTLS)
	if err != nil || tlsMux == nil {
		return nil, err
	}
	wssMux, err := getSharedPortMux(routeBridgeWSS)
	if err != nil || wssMux == nil {
		return nil, err
	}
	if tlsMux != wssMux {
		return nil, nil
	}
	logs.Info("server start, bridge reserved tls gateway is enabled on port %d", BridgeTlsPort)
	return tlsMux.GetClientReservedTLSListener(), nil
}

func GetHttpListener() (net.Listener, error) {
	if mux, err := getSharedPortMux(routeHTTPProxy); err != nil {
		return nil, err
	} else if mux != nil {
		logs.Info("start http listener, port is %d", HttpPort)
		return mux.GetHttpListener(), nil
	}
	logs.Info("start http listener, port is %d", HttpPort)
	return getTCPListener(HttpIp, HttpPort)
}

func GetHttpsListener() (net.Listener, error) {
	if mux, err := getSharedPortMux(routeHTTPS); err != nil {
		return nil, err
	} else if mux != nil {
		logs.Info("start https listener, port is %d", HttpsPort)
		return mux.GetHttpsListener(), nil
	}
	logs.Info("start https listener, port is %d", HttpsPort)
	return getTCPListener(HttpIp, HttpsPort)
}

func GetWebManagerListener() (net.Listener, error) {
	if mux, err := getSharedPortMux(routeWeb); err != nil {
		return nil, err
	} else if mux != nil {
		logs.Info("Web management start, access port is %d", WebPort)
		if WebOpenSSL {
			return mux.GetManagerTLSListener(), nil
		}
		return mux.GetManagerListener(), nil
	}
	logs.Info("web management start, access port is %d", WebPort)
	return getTCPListener(WebIp, WebPort)
}

func getTCPListener(ip string, port int) (net.Listener, error) {
	if err := validateTCPPort(port, "tcp"); err != nil {
		return nil, err
	}
	if ip == "" {
		ip = "0.0.0.0"
	}
	return net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP(ip), Port: port})
}

func validateTCPPort(port int, target string) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid %s port %d", target, port)
	}
	return nil
}

type sharedRouteKind string

const (
	routeBridgeTCP sharedRouteKind = "bridge_tcp"
	routeBridgeTLS sharedRouteKind = "bridge_tls"
	routeBridgeWS  sharedRouteKind = "bridge_ws"
	routeBridgeWSS sharedRouteKind = "bridge_wss"
	routeHTTPProxy sharedRouteKind = "http_proxy"
	routeHTTPS     sharedRouteKind = "https_proxy"
	routeWeb       sharedRouteKind = "web_manager"
)

type listenKey struct {
	ip   string
	port int
}

func (k listenKey) String() string {
	return net.JoinHostPort(k.ip, strconv.Itoa(k.port))
}

type routeSpec struct {
	kind sharedRouteKind
	key  listenKey
}

type sharedMuxConfig struct {
	ManagerHost string
	ClientHost  string
	WebOpenSSL  bool
	BridgePath  string
	Routes      []routeSpec
}

type portMuxManager struct {
	muxByKey   map[listenKey]*pmux.PortMux
	muxByRoute map[sharedRouteKind]*pmux.PortMux
}

func (m *portMuxManager) close() {
	if m == nil {
		return
	}
	for _, mux := range m.muxByKey {
		_ = mux.Close()
	}
}

var sharedMuxManager *portMuxManager
var sharedMuxErr error

func normalizeBindIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "0.0.0.0"
	}
	return ip
}

func resetSharedMuxManager() {
	if sharedMuxManager != nil {
		sharedMuxManager.close()
	}
	sharedMuxManager = nil
	sharedMuxErr = nil
}

func rebuildSharedMuxManager(cfg sharedMuxConfig) {
	resetSharedMuxManager()

	routes := cfg.Routes
	if len(routes) == 0 {
		return
	}
	if err := validateRouteSpecs(routes); err != nil {
		sharedMuxErr = err
		return
	}

	groups := groupRouteSpecs(routes)
	if err := validateSharedMuxGroups(groups, cfg); err != nil {
		sharedMuxErr = err
		return
	}

	manager := &portMuxManager{
		muxByKey:   make(map[listenKey]*pmux.PortMux),
		muxByRoute: make(map[sharedRouteKind]*pmux.PortMux),
	}
	for key, group := range groups {
		if len(group) < 2 {
			continue
		}
		mux := pmux.NewPortMux(key.ip, key.port, cfg.ManagerHost, cfg.ClientHost, cfg.BridgePath)
		if err := mux.Start(); err != nil {
			sharedMuxErr = err
			_ = mux.Close()
			manager.close()
			return
		}
		manager.muxByKey[key] = mux
		for _, route := range group {
			manager.muxByRoute[route.kind] = mux
		}
	}
	if len(manager.muxByKey) == 0 {
		return
	}
	sharedMuxManager = manager
}

func sharedMuxConfigFromConfig(cfg Config) sharedMuxConfig {
	return sharedMuxConfig{
		ManagerHost: cfg.WebHost,
		ClientHost:  cfg.BridgeHost,
		WebOpenSSL:  cfg.WebOpenSSL,
		BridgePath:  cfg.BridgePath,
		Routes:      collectRouteSpecs(cfg),
	}
}

func collectRouteSpecs(cfg Config) []routeSpec {
	routes := make([]routeSpec, 0, 7)
	appendRoute := func(kind sharedRouteKind, ip string, port int) {
		if port <= 0 {
			return
		}
		routes = append(routes, routeSpec{
			kind: kind,
			key: listenKey{
				ip:   normalizeBindIP(ip),
				port: port,
			},
		})
	}

	appendRoute(routeBridgeTCP, cfg.BridgeTcpIp, cfg.BridgeTcpPort)
	appendRoute(routeBridgeTLS, cfg.BridgeTlsIp, cfg.BridgeTlsPort)
	appendRoute(routeBridgeWS, cfg.BridgeWsIp, cfg.BridgeWsPort)
	appendRoute(routeBridgeWSS, cfg.BridgeWssIp, cfg.BridgeWssPort)
	appendRoute(routeHTTPProxy, cfg.HttpIp, cfg.HttpPort)
	appendRoute(routeHTTPS, cfg.HttpIp, cfg.HttpsPort)
	appendRoute(routeWeb, cfg.WebIp, cfg.WebPort)
	return routes
}

func validateRouteSpecs(routes []routeSpec) error {
	ipsByPort := make(map[int]map[string]struct{})
	for _, route := range routes {
		if _, ok := ipsByPort[route.key.port]; !ok {
			ipsByPort[route.key.port] = make(map[string]struct{})
		}
		ipsByPort[route.key.port][route.key.ip] = struct{}{}
	}
	for port, ips := range ipsByPort {
		if len(ips) < 2 {
			continue
		}
		values := make([]string, 0, len(ips))
		for ip := range ips {
			values = append(values, ip)
		}
		sort.Strings(values)
		return fmt.Errorf("shared TCP listener on port %d requires the same bind ip, got %s", port, strings.Join(values, ", "))
	}
	return nil
}

func groupRouteSpecs(routes []routeSpec) map[listenKey][]routeSpec {
	groups := make(map[listenKey][]routeSpec)
	for _, route := range routes {
		groups[route.key] = append(groups[route.key], route)
	}
	return groups
}

func validateSharedMuxGroups(groups map[listenKey][]routeSpec, cfg sharedMuxConfig) error {
	for key, group := range groups {
		if len(group) < 2 {
			continue
		}
		if err := validateSharedMuxGroup(key, group, cfg); err != nil {
			return err
		}
	}
	return nil
}

func validateSharedMuxGroup(key listenKey, group []routeSpec, cfg sharedMuxConfig) error {
	kinds := make(map[sharedRouteKind]struct{}, len(group))
	for _, route := range group {
		kinds[route.kind] = struct{}{}
	}
	managerHost := normalizeSharedHost(cfg.ManagerHost)
	clientHost := normalizeSharedHost(cfg.ClientHost)
	bridgeWS := hasRoute(kinds, routeBridgeWS)
	bridgeTLS := hasRoute(kinds, routeBridgeTLS)
	bridgeWSS := hasRoute(kinds, routeBridgeWSS)
	httpProxy := hasRoute(kinds, routeHTTPProxy)
	httpsProxy := hasRoute(kinds, routeHTTPS)
	webManager := hasRoute(kinds, routeWeb)
	webSharesHTTP := webManager && !cfg.WebOpenSSL
	webSharesTLS := webManager && cfg.WebOpenSSL
	bridgePathSet := strings.TrimSpace(cfg.BridgePath) != ""
	managerUsesReservedHost := webSharesHTTP && (httpProxy || bridgeWS) || webSharesTLS && (httpsProxy || bridgeTLS || bridgeWSS)
	clientUsesReservedHost := bridgeWS && (httpProxy || webSharesHTTP) || bridgeTLS && (httpsProxy || bridgeWSS || webSharesTLS) || bridgeWSS

	if webSharesHTTP && (httpProxy || bridgeWS) && managerHost == "" {
		return fmt.Errorf("shared TCP listener %s requires web_host when web manager shares HTTP traffic", key)
	}
	if webSharesTLS && (httpsProxy || bridgeTLS || bridgeWSS) && managerHost == "" {
		return fmt.Errorf("shared TCP listener %s requires web_host when web manager shares TLS traffic", key)
	}
	if managerUsesReservedHost {
		if err := validateReservedSharedHost(key, "web_host", managerHost); err != nil {
			return err
		}
	}
	if bridgeWS && (httpProxy || webSharesHTTP) {
		if clientHost == "" {
			return fmt.Errorf("shared TCP listener %s requires bridge_host when bridge ws shares HTTP traffic", key)
		}
		if !bridgePathSet {
			return fmt.Errorf("shared TCP listener %s requires bridge_path when bridge ws shares HTTP traffic", key)
		}
	}
	if bridgeTLS && (httpsProxy || bridgeWSS || webSharesTLS) && clientHost == "" {
		return fmt.Errorf("shared TCP listener %s requires bridge_host when bridge tls shares TLS traffic", key)
	}
	if bridgeWSS {
		if (httpsProxy || bridgeTLS || webSharesTLS) && clientHost == "" {
			return fmt.Errorf("shared TCP listener %s requires bridge_host when bridge wss shares TLS traffic", key)
		}
		if !bridgePathSet {
			return fmt.Errorf("shared TCP listener %s requires bridge_path when bridge wss is enabled", key)
		}
	}
	if bridgeTLS && bridgeWSS && clientHost == "" {
		return fmt.Errorf("shared TCP listener %s requires bridge_host when bridge tls and bridge wss share the same TLS listener", key)
	}
	if clientUsesReservedHost {
		if err := validateReservedSharedHost(key, "bridge_host", clientHost); err != nil {
			return err
		}
	}
	if webSharesHTTP && bridgeWS && managerHost != "" && clientHost != "" && strings.EqualFold(managerHost, clientHost) {
		return fmt.Errorf("shared TCP listener %s requires web_host and bridge_host to differ for shared HTTP traffic", key)
	}
	if webSharesTLS && (bridgeTLS || bridgeWSS) &&
		managerHost != "" && clientHost != "" && strings.EqualFold(managerHost, clientHost) {
		return fmt.Errorf("shared TCP listener %s requires web_host and bridge_host to differ for shared TLS traffic", key)
	}
	if bridgeWS && !bridgePathSet {
		return fmt.Errorf("shared TCP listener %s requires bridge_path when bridge ws is enabled", key)
	}
	return nil
}

func hasRoute(kinds map[sharedRouteKind]struct{}, target sharedRouteKind) bool {
	_, ok := kinds[target]
	return ok
}

func getSharedPortMux(kind sharedRouteKind) (*pmux.PortMux, error) {
	if sharedMuxErr != nil {
		return nil, sharedMuxErr
	}
	if sharedMuxManager == nil {
		return nil, nil
	}
	return sharedMuxManager.muxByRoute[kind], nil
}

func normalizeSharedHost(host string) string {
	return strings.TrimSpace(common.GetIpByAddr(strings.TrimSpace(host)))
}

func validateReservedSharedHost(key listenKey, fieldName, host string) error {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		return nil
	}
	if strings.Contains(trimmed, "*") {
		return fmt.Errorf("shared TCP listener %s requires %s to be an exact host, wildcard is not supported", key, fieldName)
	}
	if normalizeSharedHost(trimmed) == "" {
		return fmt.Errorf("shared TCP listener %s requires %s to be a valid host", key, fieldName)
	}
	return nil
}
