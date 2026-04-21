//go:build !sdk

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/version"
	flag "github.com/spf13/pflag"
)

type launchPreparedBundle struct {
	Label   string
	Runners []launchPreparedRunner
}

type launchPreparedRunner struct {
	ID          string
	Label       string
	DirectNodes []launchPreparedDirectNode
	Config      *config.Config
	Local       *client.LaunchLocal
}

type launchPreparedDirectNode struct {
	Server  string
	VKey    string
	Type    string
	Proxy   string
	LocalIP string
}

type launchRunResult struct {
	ID        string
	Label     string
	Err       error
	Reconnect bool
	UUID      string
}

type launchSourceFailurePlan struct {
	Status      string
	Source      string
	Delay       time.Duration
	UseLastGood bool
}

func startLaunchInputs(ctx context.Context, cancel context.CancelFunc, inputs []string, runtime *client.LaunchRuntime) error {
	items := make([]string, 0, len(inputs))
	for _, input := range inputs {
		input = strings.TrimSpace(input)
		if input != "" {
			items = append(items, input)
		}
	}
	if len(items) == 0 {
		return fmt.Errorf("launch input is empty")
	}
	for i, input := range items {
		go superviseLaunchInput(ctx, cancel, input, i+1, runtime)
	}
	return nil
}

func superviseLaunchInput(ctx context.Context, cancel context.CancelFunc, input string, index int, runtime *client.LaunchRuntime) {
	label := fmt.Sprintf("launch input %d", index)
	uuidState := make(map[string]string)
	var lastGood *launchPreparedBundle
	var lastSourceIssue bool
	var nextSourceAttempt time.Time

	for {
		if ctx.Err() != nil {
			return
		}

		var bundle *launchPreparedBundle
		if lastGood != nil && !nextSourceAttempt.IsZero() && time.Now().Before(nextSourceAttempt) {
			logs.Warn("source_paused input=%q resume_after=%s use_last_good=true", label, nextSourceAttempt.Format(time.RFC3339))
			bundle = lastGood
		} else {
			spec, err := client.ResolveLaunchSpec(ctx, input)
			if err == nil {
				bundle, err = prepareLaunchBundle(ctx, label, spec, runtime)
			}
			if err != nil {
				plan := planLaunchSourceFailure(label, err, lastGood != nil)
				logLaunchSourceFailure(label, err, plan)
				if plan.UseLastGood && lastGood != nil {
					bundle = lastGood
					if plan.Delay > 0 {
						nextSourceAttempt = time.Now().Add(plan.Delay)
					}
				} else {
					nextSourceAttempt = time.Time{}
					if !sleepLaunchDelay(ctx, plan.Delay) {
						return
					}
					lastSourceIssue = true
					continue
				}
				lastSourceIssue = true
			} else {
				if lastSourceIssue {
					logs.Info("source_ok input=%q", label)
				}
				lastGood = bundle
				lastSourceIssue = false
				nextSourceAttempt = time.Time{}
			}
		}

		result := runPreparedBundleOnce(ctx, cancel, bundle, uuidState)
		if ctx.Err() != nil {
			return
		}
		if !result.Reconnect {
			if result.Err != nil && !errors.Is(result.Err, context.Canceled) {
				logs.Error("%s stopped: %v", result.Label, result.Err)
			}
			cancel()
			return
		}
		if result.Err != nil && !errors.Is(result.Err, context.Canceled) {
			logs.Warn("%s closed, relaunching in %s: %v", result.Label, defaultLaunchReconnectDelay, result.Err)
		} else {
			logs.Warn("%s closed, relaunching in %s", result.Label, defaultLaunchReconnectDelay)
		}
		if !sleepLaunchDelay(ctx, defaultLaunchReconnectDelay) {
			return
		}
	}
}

func prepareLaunchBundle(ctx context.Context, inputLabel string, spec *client.LaunchSpec, runtime *client.LaunchRuntime) (*launchPreparedBundle, error) {
	if spec == nil {
		return nil, fmt.Errorf("%s: launch spec is empty", inputLabel)
	}
	profiles := spec.ExpandProfiles()
	if len(profiles) == 0 {
		return nil, fmt.Errorf("%s: launch spec does not contain runnable profiles", inputLabel)
	}
	bundle := &launchPreparedBundle{
		Label:   inputLabel,
		Runners: make([]launchPreparedRunner, 0, len(profiles)),
	}
	for i := range profiles {
		profile := profiles[i]
		profileLabel := strings.TrimSpace(profile.Name)
		if profileLabel == "" {
			profileLabel = fmt.Sprintf("%s profile %d", inputLabel, i+1)
		} else {
			profileLabel = fmt.Sprintf("%s %s", inputLabel, profileLabel)
		}
		profileID := fmt.Sprintf("profile:%d", i)
		if name := strings.TrimSpace(profile.Name); name != "" {
			profileID = "profile:" + name
		}
		switch profile.Mode() {
		case "direct":
			nodes, err := buildPreparedDirectNodes(profile.Direct)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", profileLabel, err)
			}
			bundle.Runners = append(bundle.Runners, launchPreparedRunner{
				ID:          profileID,
				Label:       profileLabel,
				DirectNodes: nodes,
			})
		case "config":
			cfg, err := profile.BuildConfigWithRuntimeContext(ctx, runtime)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", profileLabel, err)
			}
			bundle.Runners = append(bundle.Runners, launchPreparedRunner{
				ID:     profileID,
				Label:  profileLabel,
				Config: cfg,
			})
		case "local":
			if profile.Local == nil {
				return nil, fmt.Errorf("%s: local launch is empty", profileLabel)
			}
			if err := profile.Local.Validate(); err != nil {
				return nil, fmt.Errorf("%s: %w", profileLabel, err)
			}
			bundle.Runners = append(bundle.Runners, launchPreparedRunner{
				ID:    profileID,
				Label: profileLabel,
				Local: profile.Local,
			})
		default:
			return nil, fmt.Errorf("%s: unsupported launch mode", profileLabel)
		}
	}
	return bundle, nil
}

func buildPreparedDirectNodes(direct *client.LaunchDirect) ([]launchPreparedDirectNode, error) {
	if direct == nil {
		return nil, fmt.Errorf("direct launch is empty")
	}
	if err := direct.Validate(); err != nil {
		return nil, err
	}
	serverAddrs := common.HandleArrEmptyVal(strings.Split(strings.ReplaceAll(direct.Server.JoinComma(), "，", ","), ","))
	verifyKeys := common.HandleArrEmptyVal(strings.Split(strings.ReplaceAll(direct.VKey.JoinComma(), "，", ","), ","))
	connTypes := common.HandleArrEmptyVal(strings.Split(strings.ReplaceAll(direct.Type.JoinComma(), "，", ","), ","))
	localIPs := common.HandleArrEmptyVal(strings.Split(strings.ReplaceAll(direct.LocalIP.JoinComma(), "，", ","), ","))
	if len(connTypes) == 0 {
		connTypes = append(connTypes, "tcp")
	}
	if len(serverAddrs) == 0 || len(verifyKeys) == 0 || serverAddrs[0] == "" || verifyKeys[0] == "" {
		return nil, fmt.Errorf("serverAddr or verifyKey cannot be empty")
	}
	maxLength := common.ExtendArrs(&serverAddrs, &verifyKeys, &connTypes, &localIPs)
	nodes := make([]launchPreparedDirectNode, 0, maxLength)
	for i := 0; i < maxLength; i++ {
		nodes = append(nodes, launchPreparedDirectNode{
			Server:  serverAddrs[i],
			VKey:    verifyKeys[i],
			Type:    strings.ToLower(connTypes[i]),
			Proxy:   direct.Proxy,
			LocalIP: localIPs[i],
		})
	}
	return nodes, nil
}

func runPreparedBundleOnce(ctx context.Context, cancel context.CancelFunc, bundle *launchPreparedBundle, uuidState map[string]string) launchRunResult {
	if bundle == nil || len(bundle.Runners) == 0 {
		return launchRunResult{Label: "launch bundle", Err: fmt.Errorf("launch bundle is empty"), Reconnect: false}
	}
	childCtx, childCancel := context.WithCancel(ctx)
	defer childCancel()
	localManagers := newLaunchLocalManagerRegistry(childCtx, cancel)

	results := make(chan launchRunResult, len(bundle.Runners))
	var wg sync.WaitGroup
	for _, runner := range bundle.Runners {
		runner := runner
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- runPreparedRunnerOnce(childCtx, childCancel, cancel, localManagers, runner, uuidState[runner.ID])
		}()
	}

	var primary launchRunResult
	select {
	case <-ctx.Done():
		primary = launchRunResult{Label: bundle.Label, Err: ctx.Err(), Reconnect: false}
	case primary = <-results:
	}
	childCancel()
	wg.Wait()
	close(results)
	for result := range results {
		if result.UUID != "" {
			uuidState[result.ID] = result.UUID
		}
	}
	if primary.UUID != "" {
		uuidState[primary.ID] = primary.UUID
	}
	return primary
}

func runPreparedRunnerOnce(ctx context.Context, childCancel context.CancelFunc, parentCancel context.CancelFunc, localManagers *launchLocalManagerRegistry, runner launchPreparedRunner, uuid string) launchRunResult {
	switch {
	case len(runner.DirectNodes) > 0:
		return runPreparedDirectOnce(ctx, runner)
	case runner.Config != nil:
		return runPreparedConfigOnce(ctx, childCancel, runner, uuid)
	case runner.Local != nil:
		return runPreparedLocalOnce(ctx, parentCancel, localManagers, runner)
	default:
		return launchRunResult{ID: runner.ID, Label: runner.Label, Err: fmt.Errorf("launch runner is empty"), Reconnect: false}
	}
}

func runPreparedDirectOnce(ctx context.Context, runner launchPreparedRunner) launchRunResult {
	logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, version.GetVersion(*protoVer))
	common.SyncTime()

	groupCtx, groupCancel := context.WithCancel(ctx)
	defer groupCancel()

	results := make(chan launchRunResult, len(runner.DirectNodes))
	var wg sync.WaitGroup
	for _, node := range runner.DirectNodes {
		node := node
		wg.Add(1)
		go func() {
			defer wg.Done()
			logs.Info("Start server: %s vkey: %s type: %s local_ip: %s", node.Server, node.VKey, node.Type, node.LocalIP)
			client.NewRPClient(node.Server, node.VKey, node.Type, node.Proxy, node.LocalIP, "", nil, *disconnectTime, nil).Start(groupCtx)
			if groupCtx.Err() != nil {
				results <- launchRunResult{ID: runner.ID, Label: runner.Label, Err: ctx.Err(), Reconnect: false}
				return
			}
			results <- launchRunResult{
				ID:        runner.ID,
				Label:     fmt.Sprintf("%s [%s]", runner.Label, node.Server),
				Err:       fmt.Errorf("direct connection closed"),
				Reconnect: *autoReconnect,
			}
		}()
	}

	var result launchRunResult
	select {
	case <-ctx.Done():
		result = launchRunResult{ID: runner.ID, Label: runner.Label, Err: ctx.Err(), Reconnect: false}
	case result = <-results:
	}
	groupCancel()
	wg.Wait()
	return result
}

func runPreparedConfigOnce(ctx context.Context, cancel context.CancelFunc, runner launchPreparedRunner, uuid string) launchRunResult {
	reconnect := client.AutoReconnect
	if runner.Config != nil && runner.Config.CommonConfig != nil {
		reconnect = reconnect && runner.Config.CommonConfig.AutoReconnection
	}
	nextUUID, err := client.RunConfigOnce(ctx, cancel, runner.Config, runner.Label, uuid)
	if ctx.Err() != nil {
		return launchRunResult{ID: runner.ID, Label: runner.Label, UUID: nextUUID, Err: ctx.Err(), Reconnect: false}
	}
	return launchRunResult{ID: runner.ID, Label: runner.Label, UUID: nextUUID, Err: err, Reconnect: reconnect}
}

func runPreparedLocalOnce(ctx context.Context, cancel context.CancelFunc, localManagers *launchLocalManagerRegistry, runner launchPreparedRunner) launchRunResult {
	logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, version.GetVersion(*protoVer))
	common.SyncTime()
	commonConfig, localServer := buildLaunchLocalConfig(runner.Local)
	applyLaunchLocalRuntimeDefaults(commonConfig)
	p2pm := localManagers.managerFor(commonConfig)
	if p2pm == nil {
		p2pm = client.NewP2PManager(ctx, cancel, commonConfig)
	}
	if err := p2pm.StartLocalServer(localServer); err != nil {
		if ctx.Err() != nil {
			return launchRunResult{ID: runner.ID, Label: runner.Label, Err: ctx.Err(), Reconnect: false}
		}
		return launchRunResult{ID: runner.ID, Label: runner.Label, Err: err, Reconnect: *autoReconnect}
	}
	<-ctx.Done()
	return launchRunResult{ID: runner.ID, Label: runner.Label, Err: ctx.Err(), Reconnect: false}
}

func planLaunchSourceFailure(label string, err error, hasLastGood bool) launchSourceFailurePlan {
	plan := launchSourceFailurePlan{
		Status:      "source_invalid",
		Source:      label,
		Delay:       defaultLaunchSourceRetryDelay,
		UseLastGood: hasLastGood,
	}
	var sourceErr *client.LaunchSourceError
	if !errors.As(err, &sourceErr) || sourceErr == nil {
		return plan
	}
	if sourceErr.Source != "" {
		plan.Source = sourceErr.Source
	}
	switch {
	case sourceErr.Revoked:
		plan.Status = "source_revoked"
		plan.Delay = launchSourceDelay(sourceErr.RetryAfter, defaultLaunchSourcePauseDelay)
		plan.UseLastGood = false
	case sourceErr.Temporary:
		plan.Status = "source_retry"
		plan.Delay = launchSourceDelay(sourceErr.RetryAfter, defaultLaunchSourceRetryDelay)
	case sourceErr.Invalid:
		plan.Status = "source_invalid"
		plan.Delay = defaultLaunchSourceRetryDelay
	case sourceErr.StatusCode >= 400 && sourceErr.StatusCode < 500:
		plan.Status = "source_paused"
		plan.Delay = launchSourceDelay(sourceErr.RetryAfter, defaultLaunchSourcePauseDelay)
		plan.UseLastGood = false
	default:
		plan.Status = "source_retry"
		plan.Delay = launchSourceDelay(sourceErr.RetryAfter, defaultLaunchSourceRetryDelay)
	}
	return plan
}

func logLaunchSourceFailure(label string, err error, plan launchSourceFailurePlan) {
	switch plan.Status {
	case "source_retry":
		logs.Warn("%s input=%q source=%q retry_in=%s use_last_good=%t err=%v", plan.Status, label, plan.Source, plan.Delay, plan.UseLastGood, err)
	case "source_revoked":
		logs.Warn("%s input=%q source=%q retry_in=%s err=%v", plan.Status, label, plan.Source, plan.Delay, err)
		logs.Warn("source_paused input=%q source=%q retry_in=%s", label, plan.Source, plan.Delay)
	case "source_paused":
		logs.Warn("%s input=%q source=%q retry_in=%s err=%v", plan.Status, label, plan.Source, plan.Delay, err)
	default:
		logs.Warn("source_invalid input=%q source=%q retry_in=%s use_last_good=%t err=%v", label, plan.Source, plan.Delay, plan.UseLastGood, err)
	}
}

func launchSourceDelay(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func sleepLaunchDelay(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
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

func applyLaunchRuntime(runtime *client.LaunchRuntime) {
	if runtime == nil {
		return
	}
	setStringFlag("log", logType, runtime.Log)
	setStringFlag("log_level", logLevel, runtime.LogLevel)
	setStringFlag("log_path", logPath, runtime.LogPath)
	setStringFlag("pprof", pprofAddr, runtime.PProf)
	setStringFlag("dns_server", dnsServer, runtime.DNSServer)
	setStringFlag("ntp_server", ntpServer, runtime.NTPServer)
	setStringFlag("timezone", timezone, runtime.Timezone)
	setStringFlag("p2p_type", p2pType, runtime.P2PType)
	setBoolFlag("debug", debug, runtime.Debug)
	setIntFlag("log_max_size", logMaxSize, runtime.LogMaxSize)
	setIntFlag("log_max_days", logMaxDays, runtime.LogMaxDays)
	setIntFlag("log_max_files", logMaxFiles, runtime.LogMaxFiles)
	setBoolFlag("log_compress", logCompress, runtime.LogCompress)
	setBoolFlag("log_color", logColor, runtime.LogColor)
	setIntFlag("proto_version", protoVer, runtime.ProtoVersion)
	setBoolFlag("skip_verify", skipVerify, runtime.SkipVerify)
	setIntFlag("keepalive", keepAlive, runtime.KeepAlive)
	setIntFlag("ntp_interval", ntpInterval, runtime.NTPInterval)
	setBoolFlag("disable_p2p", disableP2P, runtime.DisableP2P)
	setBoolFlag("local_ip_forward", localIPForward, runtime.LocalIPForward)
	setBoolFlag("auto_reconnect", autoReconnect, runtime.AutoReconnect)
	setIntFlag("disconnect_timeout", disconnectTime, runtime.DisconnectTimeout)
	setIntFlag("p2p_timeout", p2pTime, runtime.P2PTimeout)
}

func applyLaunchDirect(direct *client.LaunchDirect) {
	if direct == nil {
		return
	}
	if direct.Server.HasValue() && !flagChanged("server") {
		*serverAddr = direct.Server.JoinComma()
	}
	if direct.VKey.HasValue() && !flagChanged("vkey") {
		*verifyKey = direct.VKey.JoinComma()
	}
	if direct.Type.HasValue() && !flagChanged("type") {
		*connType = direct.Type.JoinComma()
	}
	if strings.TrimSpace(direct.Proxy) != "" && !flagChanged("proxy") {
		*proxyUrl = strings.TrimSpace(direct.Proxy)
	}
	if direct.LocalIP.HasValue() && !flagChanged("local_ip") {
		*localIP = direct.LocalIP.JoinComma()
	}
}

func applyLaunchLocal(local *client.LaunchLocal) {
	if local == nil {
		return
	}
	if strings.TrimSpace(local.Server) != "" && !flagChanged("server") {
		*serverAddr = strings.TrimSpace(local.Server)
	}
	if strings.TrimSpace(local.VKey) != "" && !flagChanged("vkey") {
		*verifyKey = strings.TrimSpace(local.VKey)
	}
	if strings.TrimSpace(local.Type) != "" && !flagChanged("type") {
		*connType = strings.TrimSpace(local.Type)
	}
	if strings.TrimSpace(local.Proxy) != "" && !flagChanged("proxy") {
		*proxyUrl = strings.TrimSpace(local.Proxy)
	}
	if strings.TrimSpace(local.LocalIP) != "" && !flagChanged("local_ip") {
		*localIP = strings.TrimSpace(local.LocalIP)
	}
	if strings.TrimSpace(local.LocalType) != "" && !flagChanged("local_type") {
		*localType = strings.TrimSpace(local.LocalType)
	}
	setIntFlag("local_port", localPort, local.LocalPort)
	if strings.TrimSpace(local.Password) != "" && !flagChanged("password") {
		*password = strings.TrimSpace(local.Password)
	}
	if resolvedTarget := local.NormalizedTarget(); resolvedTarget != "" && !flagChanged("target") {
		*target = resolvedTarget
	}
	if strings.TrimSpace(local.TargetType) != "" && !flagChanged("target_type") {
		*targetType = strings.TrimSpace(local.TargetType)
	}
	setBoolFlag("fallback_secret", fallbackSecret, local.FallbackSecret)
	setBoolFlag("local_proxy", localProxy, local.LocalProxy)
}

func flagChanged(name string) bool {
	f := flag.CommandLine.Lookup(name)
	return f != nil && f.Changed
}

func setStringFlag(name string, target *string, value *string) {
	if target == nil || value == nil || flagChanged(name) {
		return
	}
	*target = *value
}

func setBoolFlag(name string, target *bool, value *bool) {
	if target == nil || value == nil || flagChanged(name) {
		return
	}
	*target = *value
}

func setIntFlag(name string, target *int, value *int) {
	if target == nil || value == nil || flagChanged(name) {
		return
	}
	*target = *value
}

func hasExplicitLegacyModeFlags() bool {
	for _, name := range []string{
		"server", "vkey", "type", "config", "proxy", "local_ip",
		"local_type", "local_port", "password", "target", "target_type",
		"fallback_secret", "local_proxy",
	} {
		if flagChanged(name) {
			return true
		}
	}
	return false
}

func startLaunchDirect(ctx context.Context, cancel context.CancelFunc, server, vkey, tp, proxy, local string) error {
	if tp == "" {
		tp = "tcp"
	}

	logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, version.GetVersion(*protoVer))
	common.SyncTime()

	server = strings.ReplaceAll(server, "，", ",")
	server = strings.ReplaceAll(server, "：", ":")
	vkey = strings.ReplaceAll(vkey, "，", ",")
	tp = strings.ReplaceAll(tp, "，", ",")
	local = strings.ReplaceAll(local, "，", ",")

	serverAddrs := common.HandleArrEmptyVal(strings.Split(server, ","))
	verifyKeys := common.HandleArrEmptyVal(strings.Split(vkey, ","))
	connTypes := common.HandleArrEmptyVal(strings.Split(tp, ","))
	localIPs := common.HandleArrEmptyVal(strings.Split(local, ","))

	if len(connTypes) == 0 {
		connTypes = append(connTypes, "tcp")
	}
	if len(serverAddrs) == 0 || len(verifyKeys) == 0 || serverAddrs[0] == "" || verifyKeys[0] == "" {
		return fmt.Errorf("serverAddr or verifyKey cannot be empty")
	}

	maxLength := common.ExtendArrs(&serverAddrs, &verifyKeys, &connTypes, &localIPs)
	for i := 0; i < maxLength; i++ {
		serverAddr := serverAddrs[i]
		verifyKey := verifyKeys[i]
		connType := strings.ToLower(connTypes[i])
		localIP := localIPs[i]
		go func(serverAddr, verifyKey, connType, localIP, proxy string) {
			for {
				logs.Info("Start server: %s vkey: %s type: %s local_ip: %s", serverAddr, verifyKey, connType, localIP)
				client.NewRPClient(serverAddr, verifyKey, connType, proxy, localIP, "", nil, *disconnectTime, nil).Start(ctx)
				if *autoReconnect {
					logs.Info("Client closed! It will be reconnected in five seconds")
					if !sleepLaunchDelay(ctx, defaultLaunchReconnectDelay) {
						return
					}
				} else {
					logs.Info("Client closed!")
					cancel()
					os.Exit(1)
					return
				}
			}
		}(serverAddr, verifyKey, connType, localIP, proxy)
	}
	return nil
}

func startLaunchLocal(ctx context.Context, cancel context.CancelFunc, local *client.LaunchLocal) error {
	if local == nil {
		return fmt.Errorf("local launch is empty")
	}
	logs.Info("the version of client is %s, the core version of client is %s", version.VERSION, version.GetVersion(*protoVer))
	common.SyncTime()
	commonConfig, localServer := buildLaunchLocalConfig(local)
	applyLaunchLocalRuntimeDefaults(commonConfig)
	p2pm := client.NewP2PManager(ctx, cancel, commonConfig)
	return p2pm.StartLocalServer(localServer)
}

func buildLaunchLocalConfig(local *client.LaunchLocal) (*config.CommonConfig, *config.LocalServer) {
	commonConfig := new(config.CommonConfig)
	commonConfig.Server = strings.TrimSpace(local.Server)
	commonConfig.VKey = strings.TrimSpace(local.VKey)
	commonConfig.Tp = strings.ToLower(strings.TrimSpace(local.Type))
	if commonConfig.Tp == "" {
		commonConfig.Tp = "tcp"
	}
	commonConfig.ProxyUrl = strings.TrimSpace(local.Proxy)
	commonConfig.LocalIP = strings.TrimSpace(local.LocalIP)
	localServer := &config.LocalServer{
		Type:       strings.ToLower(strings.TrimSpace(local.LocalType)),
		Password:   strings.TrimSpace(local.Password),
		Target:     local.NormalizedTarget(),
		TargetType: strings.ToLower(strings.TrimSpace(local.TargetType)),
	}
	if localServer.Type == "" {
		localServer.Type = "p2p"
	}
	if localServer.TargetType == "" {
		localServer.TargetType = "all"
	}
	if local.LocalPort != nil {
		localServer.Port = *local.LocalPort
	}
	if local.FallbackSecret != nil {
		localServer.Fallback = *local.FallbackSecret
	}
	if local.LocalProxy != nil {
		localServer.LocalProxy = *local.LocalProxy
	}
	commonConfig.Client = new(file.Client)
	commonConfig.Client.Cnf = new(file.Config)
	return commonConfig, localServer
}

func applyLaunchLocalRuntimeDefaults(commonConfig *config.CommonConfig) {
	if commonConfig == nil {
		return
	}
	if disconnectTime != nil {
		commonConfig.DisconnectTime = *disconnectTime
	}
}

type launchLocalManagerRegistry struct {
	ctx          context.Context
	parentCancel context.CancelFunc
	mu           sync.Mutex
	managers     map[string]*client.P2PManager
}

func newLaunchLocalManagerRegistry(ctx context.Context, parentCancel context.CancelFunc) *launchLocalManagerRegistry {
	if parentCancel == nil {
		parentCancel = func() {}
	}
	return &launchLocalManagerRegistry{
		ctx:          ctx,
		parentCancel: parentCancel,
		managers:     make(map[string]*client.P2PManager),
	}
}

func (r *launchLocalManagerRegistry) managerFor(commonConfig *config.CommonConfig) *client.P2PManager {
	if r == nil {
		return nil
	}
	key := launchLocalManagerKey(commonConfig)
	r.mu.Lock()
	defer r.mu.Unlock()
	if mgr, ok := r.managers[key]; ok && mgr != nil {
		return mgr
	}
	mgr := client.NewP2PManager(r.ctx, r.parentCancel, commonConfig)
	r.managers[key] = mgr
	return mgr
}

func launchLocalManagerKey(commonConfig *config.CommonConfig) string {
	if commonConfig == nil {
		return ""
	}
	parts := []string{
		strings.TrimSpace(commonConfig.Server),
		strings.TrimSpace(commonConfig.VKey),
		strings.ToLower(strings.TrimSpace(commonConfig.Tp)),
		strings.TrimSpace(commonConfig.ProxyUrl),
		strings.TrimSpace(commonConfig.LocalIP),
	}
	return strings.Join(parts, "|")
}

func launchCommandArgs() (server, vkey, tp, proxy, ip string, err error) {
	if resolvedLaunch != nil && resolvedLaunch.HasProfiles() {
		return profileLaunchCommandArgs(resolvedLaunch.ExpandProfiles())
	}
	if canUseResolvedConfigCommandArgs() {
		server, vkey, tp, proxy, ip = configLaunchCommandArgs(resolvedLaunch.Config.Common)
		return server, vkey, tp, proxy, ip, nil
	}
	return *serverAddr, *verifyKey, *connType, *proxyUrl, *localIP, nil
}

func profileLaunchCommandArgs(profiles []client.LaunchProfile) (server, vkey, tp, proxy, ip string, err error) {
	if len(profiles) != 1 {
		return "", "", "", "", "", fmt.Errorf("status/register does not support multiple launch profiles")
	}
	profile := profiles[0]
	switch profile.Mode() {
	case "direct":
		return directLaunchCommandArgs(profile)
	case "config":
		if profile.Config == nil || profile.Config.Common == nil {
			return "", "", "", "", "", fmt.Errorf("launch profile does not contain config common settings")
		}
		server, vkey, tp, proxy, ip = configLaunchCommandArgs(profile.Config.Common)
		return server, vkey, tp, proxy, ip, nil
	case "local":
		if profile.Local == nil {
			break
		}
		server, vkey, tp, proxy, ip = localLaunchCommandArgs(profile)
		return server, vkey, tp, proxy, ip, nil
	}
	return "", "", "", "", "", fmt.Errorf("launch profile does not contain command-compatible settings")
}

func directLaunchCommandArgs(profile client.LaunchProfile) (server, vkey, tp, proxy, ip string, err error) {
	tp = profile.Direct.Type.JoinComma()
	if tp == "" {
		tp = "tcp"
	}
	return profile.Direct.Server.JoinComma(), profile.Direct.VKey.JoinComma(), tp, profile.Direct.Proxy, profile.Direct.LocalIP.JoinComma(), nil
}

func localLaunchCommandArgs(profile client.LaunchProfile) (server, vkey, tp, proxy, ip string) {
	tp = profile.Local.Type
	if tp == "" {
		tp = "tcp"
	}
	return profile.Local.Server, profile.Local.VKey, tp, profile.Local.Proxy, profile.Local.LocalIP
}

func configLaunchCommandArgs(commonCfg *client.LaunchCommon) (server, vkey, tp, proxy, ip string) {
	tp = commonCfg.ConnType
	if tp == "" {
		tp = "tcp"
	}
	return commonCfg.ServerAddr, commonCfg.VKey, tp, commonCfg.ProxyURL, commonCfg.LocalIP
}

func canUseResolvedConfigCommandArgs() bool {
	return resolvedLaunch != nil &&
		resolvedLaunch.Mode() == "config" &&
		resolvedLaunch.Config != nil &&
		resolvedLaunch.Config.Common != nil &&
		!flagChanged("server") &&
		!flagChanged("vkey") &&
		!flagChanged("type") &&
		!flagChanged("proxy") &&
		!flagChanged("local_ip") &&
		strings.TrimSpace(*serverAddr) == "" &&
		strings.TrimSpace(*verifyKey) == ""
}

func resolveLaunchFlags() error {
	resolvedLaunch = nil
	launchResolveErr = nil
	inputs := currentLaunchInputs()
	if len(inputs) == 0 {
		return nil
	}
	spec, err := client.ResolveLaunchInputs(context.Background(), inputs)
	if err != nil {
		return fmt.Errorf("resolve launch payload: %w", err)
	}
	resolvedLaunch = spec
	applyLaunchRuntime(spec.Runtime)
	switch spec.Mode() {
	case "direct":
		applyLaunchDirect(spec.Direct)
	case "local":
		applyLaunchLocal(spec.Local)
	}
	return nil
}

func currentLaunchInputs() []string {
	if len(*launchPayloads) > 0 {
		items := make([]string, 0, len(*launchPayloads))
		for _, item := range *launchPayloads {
			item = strings.TrimSpace(item)
			if item != "" {
				items = append(items, item)
			}
		}
		return items
	}
	if env := strings.TrimSpace(common.GetEnvMap()["NPC_LAUNCH"]); env != "" {
		return []string{env}
	}
	return nil
}

func commandNeedsLaunchResolution(cmd string) bool {
	switch cmd {
	case "start", "stop", "restart", "install", "uninstall", "update":
		return false
	default:
		return true
	}
}

func resolvedLaunchRuntime() *client.LaunchRuntime {
	if resolvedLaunch == nil {
		return nil
	}
	return resolvedLaunch.Runtime
}
