package service

import (
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/connection"
)

type BridgeEndpoint struct {
	Type string
	Addr string
	IP   string
	Port string
}

type SystemInfo struct {
	Version string
	Year    int
}

type BridgeTransport struct {
	Enabled bool
	Type    string
	IP      string
	Port    string
	Addr    string
	ALPN    string
}

type BridgeDisplay struct {
	Primary BridgeEndpoint
	Path    string
	TCP     BridgeTransport
	KCP     BridgeTransport
	TLS     BridgeTransport
	QUIC    BridgeTransport
	WS      BridgeTransport
	WSS     BridgeTransport
}

type SystemService interface {
	Info() SystemInfo
	BridgeDisplay(*servercfg.Snapshot, string) BridgeDisplay
	RegisterManagementAccess(string)
}

type DefaultSystemService struct{}

func (DefaultSystemService) Info() SystemInfo {
	return SystemInfo{
		Version: server.GetVersion(),
		Year:    server.GetCurrentYear(),
	}
}

func (DefaultSystemService) BridgeDisplay(cfg *servercfg.Snapshot, host string) BridgeDisplay {
	primary := BestBridge(cfg, host)
	cfg = servercfg.Resolve(cfg)
	bridgeCfg := connection.CurrentBridgeRuntime()
	display := BridgeDisplay{
		Primary: primary,
	}
	if cfg.Network.BridgePath != "" {
		display.Path = cfg.Bridge.Display.Path
	}
	display.TCP = newBridgeTransport(cfg.Bridge.Display.TCP.Show, "tcp", primary.IP, cfg.Bridge.Display.TCP.IP, strconv.Itoa(bridgeCfg.TCPPort), cfg.Bridge.Display.TCP.Port, "", false)
	display.KCP = newBridgeTransport(cfg.Bridge.Display.KCP.Show, "kcp", primary.IP, cfg.Bridge.Display.KCP.IP, strconv.Itoa(bridgeCfg.KCPPort), cfg.Bridge.Display.KCP.Port, "", false)
	display.TLS = newBridgeTransport(cfg.Bridge.Display.TLS.Show, "tls", primary.IP, cfg.Bridge.Display.TLS.IP, strconv.Itoa(bridgeCfg.TLSPort), cfg.Bridge.Display.TLS.Port, "", false)
	display.QUIC = newBridgeTransport(cfg.Bridge.Display.QUIC.Show, "quic", primary.IP, cfg.Bridge.Display.QUIC.IP, strconv.Itoa(bridgeCfg.QUICPort), cfg.Bridge.Display.QUIC.Port, "", false)
	display.QUIC.ALPN = defaultString(cfg.Bridge.Display.QUIC.ALPN, defaultQuicALPN())
	if display.QUIC.Enabled && display.QUIC.ALPN != "" && display.QUIC.ALPN != "nps" {
		display.QUIC.Addr += "/" + display.QUIC.ALPN
	}
	display.WS = newBridgeTransport(cfg.Bridge.Display.WS.Show, "ws", primary.IP, cfg.Bridge.Display.WS.IP, strconv.Itoa(bridgeCfg.WSPort), cfg.Bridge.Display.WS.Port, display.Path, true)
	display.WSS = newBridgeTransport(cfg.Bridge.Display.WSS.Show, "wss", primary.IP, cfg.Bridge.Display.WSS.IP, strconv.Itoa(bridgeCfg.WSSPort), cfg.Bridge.Display.WSS.Port, display.Path, true)
	return display
}

func (DefaultSystemService) RegisterManagementAccess(remoteAddr string) {
	if server.Bridge == nil {
		return
	}
	clientIP := strings.TrimSpace(common.GetIpByAddr(remoteAddr))
	if clientIP == "" {
		return
	}
	server.Bridge.Register.Store(clientIP, time.Now().Add(2*time.Hour))
}

func BestBridge(cfg *servercfg.Snapshot, host string) BridgeEndpoint {
	cfg = servercfg.Resolve(cfg)
	bridgeCfg := connection.CurrentBridgeRuntime()
	bridgeIP := common.GetIpByAddr(defaultString(cfg.Bridge.Addr, host))
	if strings.IndexByte(bridgeIP, ':') >= 0 && (!strings.HasPrefix(bridgeIP, "[") || !strings.HasSuffix(bridgeIP, "]")) {
		bridgeIP = "[" + bridgeIP + "]"
	}

	result := BridgeEndpoint{
		Type: cfg.Bridge.PrimaryType,
		IP:   bridgeIP,
		Port: strconv.Itoa(bridgeCfg.PrimaryPort),
	}
	result.Addr = result.IP + ":" + result.Port

	switch {
	case cfg.Bridge.Display.TLS.Show:
		result.Type = "tls"
		result.Port = defaultString(cfg.Bridge.Display.TLS.Port, strconv.Itoa(bridgeCfg.TLSPort))
		result.IP = defaultString(cfg.Bridge.Display.TLS.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port
	case cfg.Bridge.Display.QUIC.Show:
		result.Type = "quic"
		result.Port = defaultString(cfg.Bridge.Display.QUIC.Port, strconv.Itoa(bridgeCfg.QUICPort))
		result.IP = defaultString(cfg.Bridge.Display.QUIC.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port
		quicALPN := defaultString(cfg.Bridge.Display.QUIC.ALPN, defaultQuicALPN())
		if quicALPN != "" && quicALPN != "nps" {
			result.Addr += "/" + quicALPN
		}
	case cfg.Bridge.Display.WSS.Show:
		result.Type = "wss"
		result.Port = defaultString(cfg.Bridge.Display.WSS.Port, strconv.Itoa(bridgeCfg.WSSPort))
		result.IP = defaultString(cfg.Bridge.Display.WSS.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port + cfg.Bridge.Display.Path
	case cfg.Bridge.Display.TCP.Show:
		result.Type = "tcp"
		result.Port = defaultString(cfg.Bridge.Display.TCP.Port, strconv.Itoa(bridgeCfg.TCPPort))
		result.IP = defaultString(cfg.Bridge.Display.TCP.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port
	case cfg.Bridge.Display.KCP.Show:
		result.Type = "kcp"
		result.Port = defaultString(cfg.Bridge.Display.KCP.Port, strconv.Itoa(bridgeCfg.KCPPort))
		result.IP = defaultString(cfg.Bridge.Display.KCP.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port
	case cfg.Bridge.Display.WS.Show:
		result.Type = "ws"
		result.Port = defaultString(cfg.Bridge.Display.WS.Port, strconv.Itoa(bridgeCfg.WSPort))
		result.IP = defaultString(cfg.Bridge.Display.WS.IP, bridgeIP)
		result.Addr = result.IP + ":" + result.Port + cfg.Bridge.Display.Path
	}

	return result
}

func newBridgeTransport(enabled bool, transportType, fallbackIP, configuredIP, fallbackPort, configuredPort, path string, appendPath bool) BridgeTransport {
	if !enabled {
		return BridgeTransport{}
	}
	transport := BridgeTransport{
		Enabled: true,
		Type:    transportType,
		IP:      defaultString(configuredIP, fallbackIP),
		Port:    defaultString(configuredPort, fallbackPort),
	}
	transport.Addr = transport.IP + ":" + transport.Port
	if appendPath && path != "" {
		transport.Addr += path
	}
	return transport
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func defaultQuicALPN() string {
	quicCfg := connection.CurrentQUICRuntime()
	if len(quicCfg.ALPN) > 0 {
		return quicCfg.ALPN[0]
	}
	return "nps"
}
