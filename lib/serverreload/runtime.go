package serverreload

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/policy"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/tool"
	"github.com/djylb/nps/web/routers"
)

var replaceManagedRuntime = routers.ReplaceManagedRuntime
var setWebHandler = server.SetWebHandler

var errManagedRuntimeUnavailable = errors.New("managed runtime unavailable")

type LogSettings struct {
	Type     string
	Level    string
	Path     string
	MaxFiles int
	MaxDays  int
	MaxSize  int
	Compress bool
	Color    bool
}

func ResolveLogSettings(cfg *servercfg.Snapshot) LogSettings {
	settings := LogSettings{}
	if cfg == nil {
		return settings
	}
	settings.Type = cfg.Log.Type
	settings.Level = cfg.Log.Level
	settings.Path = cfg.Log.Path
	if settings.Path == "" || strings.EqualFold(settings.Path, "on") || strings.EqualFold(settings.Path, "true") {
		settings.Path = common.GetLogPath()
	}
	if !strings.EqualFold(settings.Path, "off") &&
		!strings.EqualFold(settings.Path, "false") &&
		!strings.EqualFold(settings.Path, "docker") &&
		settings.Path != "/dev/null" {
		if !filepath.IsAbs(settings.Path) {
			settings.Path = filepath.Join(common.GetRunPath(), settings.Path)
		}
		if common.IsWindows() {
			settings.Path = strings.ReplaceAll(settings.Path, "\\", "\\\\")
		}
	}
	settings.MaxFiles = cfg.Log.MaxFiles
	settings.MaxDays = cfg.Log.MaxDays
	settings.MaxSize = cfg.Log.MaxSize
	settings.Compress = cfg.Log.Compress
	settings.Color = cfg.Log.Color
	if runningAsService() && !strings.EqualFold(settings.Type, "off") && !strings.EqualFold(settings.Type, "both") {
		settings.Type = "file"
	}
	return settings
}

func runningAsService() bool {
	return len(os.Args) > 1 && strings.EqualFold(strings.TrimSpace(os.Args[1]), "service")
}

func ApplyCurrentConfig() error {
	return ApplyReloadedConfig(nil, servercfg.Current())
}

func ReloadCurrentConfig() error {
	previous := servercfg.Current()
	if err := servercfg.Reload(); err != nil {
		return err
	}
	return ApplyReloadedConfig(previous, servercfg.Current())
}

func ApplyReloadedConfig(previous, current *servercfg.Snapshot) error {
	current = servercfg.Resolve(current)

	applyLogs(current)
	applySystemConfig(current)
	connection.ApplySnapshot(current)
	applyBridgeConfig(current)
	ensureManagementPlatformUsers(current)
	tool.InitAllowPort()
	tool.StartSystemInfo()
	server.ClearProxyCache()
	server.ApplyRuntimeConfig(current)
	runtimeErr := applyManagedRuntime()
	warnRestartRequiredChanges(previous, current)
	return runtimeErr
}

func applyManagedRuntime() error {
	runtime := replaceManagedRuntime(nil)
	if runtime == nil {
		return errManagedRuntimeUnavailable
	}
	if runtime.Err != nil {
		return runtime.Err
	}
	setWebHandler(runtime.Handler)
	return nil
}

func applyLogs(cfg *servercfg.Snapshot) {
	settings := ResolveLogSettings(cfg)
	logs.Init(settings.Type, settings.Level, settings.Path, settings.MaxSize, settings.MaxFiles, settings.MaxDays, settings.Compress, settings.Color)
}

func applySystemConfig(cfg *servercfg.Snapshot) {
	if err := common.SetTimezone(cfg.App.Timezone); err != nil {
		logs.Warn("Set timezone error %v", err)
	}
	common.SetCustomDNS(cfg.App.DNSServer)
	common.SetNtpServer(cfg.App.NTPServer)
	common.SetNtpInterval(cfg.NTPIntervalDuration())
	common.SyncTime()

	policy.SetDefaultGeoIPPath(policy.ResolveGeoIPPath(servercfg.Path(), cfg.App.GeoIPPath))
	policy.SetDefaultGeoSitePath(policy.ResolveGeoSitePath(servercfg.Path(), cfg.App.GeoSitePath))
	file.RecompileAccessPoliciesIfLoaded()
}

func applyBridgeConfig(cfg *servercfg.Snapshot) {
	bridge.ServerSecureMode = cfg.Security.SecureMode
	if err := bridge.SetClientSelectMode(cfg.Bridge.SelectMode); err != nil {
		logs.Warn("apply bridge select mode failed: %v", err)
	}
	cert, ok := common.LoadCert(cfg.Bridge.CertFile, cfg.Bridge.KeyFile)
	if !ok {
		logs.Info("Using randomly generated certificate.")
	}
	crypt.InitTls(cert)
}

func ensureManagementPlatformUsers(cfg *servercfg.Snapshot) {
	if cfg == nil {
		return
	}
	platformBindings := make([]file.ManagementPlatformBinding, 0, len(cfg.Runtime.ManagementPlatforms))
	for _, platform := range cfg.Runtime.EnabledManagementPlatforms() {
		platformBindings = append(platformBindings, file.ManagementPlatformBinding{
			PlatformID:      platform.PlatformID,
			ServiceUsername: platform.ServiceUsername,
			Enabled:         platform.Enabled,
		})
	}
	file.EnsureManagementPlatformUsers(platformBindings)
}

func warnRestartRequiredChanges(previous, current *servercfg.Snapshot) {
	if previous == nil || current == nil {
		return
	}
	var restartFields []string
	appendIfChanged := func(name string, before, after any) {
		if !reflect.DeepEqual(before, after) {
			restartFields = append(restartFields, name)
		}
	}

	appendIfChanged("run_mode", previous.Runtime.RunMode, current.Runtime.RunMode)
	appendIfChanged("web listener", []any{previous.Network.WebIP, previous.Network.WebPort, previous.Web.OpenSSL, previous.Web.CertFile, previous.Web.KeyFile}, []any{current.Network.WebIP, current.Network.WebPort, current.Web.OpenSSL, current.Web.CertFile, current.Web.KeyFile})
	appendIfChanged("shared mux routing", []any{previous.Web.Host, previous.Network.BridgeHost}, []any{current.Web.Host, current.Network.BridgeHost})
	appendIfChanged("http/https/http3 listeners", []any{previous.Network.HTTPProxyIP, previous.Network.HTTPProxyPort, previous.Network.HTTPSProxyPort, previous.Network.HTTP3ProxyPort}, []any{current.Network.HTTPProxyIP, current.Network.HTTPProxyPort, current.Network.HTTPSProxyPort, current.Network.HTTP3ProxyPort})
	appendIfChanged("bridge listeners", []any{
		previous.Network.BridgeTCPIP, previous.Network.BridgeTCPPort,
		previous.Network.BridgeKCPIP, previous.Network.BridgeKCPPort,
		previous.Network.BridgeQUICIP, previous.Network.BridgeQUICPort,
		previous.Network.BridgeTLSIP, previous.Network.BridgeTLSPort,
		previous.Network.BridgeWSIP, previous.Network.BridgeWSPort,
		previous.Network.BridgeWSSIP, previous.Network.BridgeWSSPort,
		previous.Bridge.ServerTCPEnabled, previous.Bridge.ServerKCPEnabled,
		previous.Bridge.ServerQUICEnabled, previous.Bridge.ServerTLSEnabled, previous.Bridge.ServerWSEnabled, previous.Bridge.ServerWSSEnabled,
	}, []any{
		current.Network.BridgeTCPIP, current.Network.BridgeTCPPort,
		current.Network.BridgeKCPIP, current.Network.BridgeKCPPort,
		current.Network.BridgeQUICIP, current.Network.BridgeQUICPort,
		current.Network.BridgeTLSIP, current.Network.BridgeTLSPort,
		current.Network.BridgeWSIP, current.Network.BridgeWSPort,
		current.Network.BridgeWSSIP, current.Network.BridgeWSSPort,
		current.Bridge.ServerTCPEnabled, current.Bridge.ServerKCPEnabled,
		current.Bridge.ServerQUICEnabled, current.Bridge.ServerTLSEnabled, current.Bridge.ServerWSEnabled, current.Bridge.ServerWSSEnabled,
	})
	appendIfChanged("bridge websocket gateway", []any{previous.Network.BridgePath, previous.Network.BridgeTrustedIPs, previous.Network.BridgeRealIPHeader}, []any{current.Network.BridgePath, current.Network.BridgeTrustedIPs, current.Network.BridgeRealIPHeader})
	appendIfChanged("p2p listener", []any{previous.Network.P2PIP, previous.Network.P2PPort}, []any{current.Network.P2PIP, current.Network.P2PPort})
	appendIfChanged("quic transport", []any{previous.Network.QUICALPNList, previous.Network.QUICKeepAlivePeriod, previous.Network.QUICMaxIdleTimeout, previous.Network.QUICMaxIncomingStreams}, []any{current.Network.QUICALPNList, current.Network.QUICKeepAlivePeriod, current.Network.QUICMaxIdleTimeout, current.Network.QUICMaxIncomingStreams})
	appendIfChanged("pprof listener", []any{previous.App.PprofIP, previous.App.PprofPort}, []any{current.App.PprofIP, current.App.PprofPort})

	if len(restartFields) == 0 {
		return
	}
	logs.Warn("Config reload applied, but these changes still require restart: %s", strings.Join(restartFields, ", "))
}
