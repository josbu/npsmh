//go:build !sdk

package main

import (
	"testing"

	flag "github.com/spf13/pflag"
)

func resetNPCFlagState() {
	*launchPayloads = nil
	*serverAddr = ""
	*verifyKey = ""
	*connType = "tcp"
	*configPath = ""
	*proxyUrl = ""
	*localIP = ""
	*localType = "p2p"
	*localPort = 2000
	*password = ""
	*target = ""
	*targetType = "all"
	*fallbackSecret = true
	*localProxy = false
	*logType = "file"
	*logLevel = "trace"
	*logPath = ""
	*logMaxSize = 5
	*logMaxDays = 7
	*logMaxFiles = 10
	*logCompress = false
	*logColor = true
	*debug = true
	*pprofAddr = ""
	*protoVer = 0
	*skipVerify = false
	*keepAlive = 0
	*dnsServer = "8.8.8.8"
	*ntpServer = ""
	*ntpInterval = 5
	*timezone = ""
	*disableP2P = false
	*p2pType = "quic"
	*localIPForward = false
	*autoReconnect = true
	*disconnectTime = 30
	*p2pTime = 5
	resolvedLaunch = nil
	launchResolveErr = nil

	resetChanged := func(name string) {
		if f := flag.CommandLine.Lookup(name); f != nil {
			f.Changed = false
		}
	}
	for _, name := range []string{
		"launch", "server", "vkey", "type", "config", "proxy", "local_ip",
		"local_type", "local_port", "password", "target", "target_type",
		"fallback_secret", "local_proxy", "log", "log_level", "log_path",
		"log_max_size", "log_max_days", "log_max_files", "log_compress",
		"log_color", "debug", "pprof", "proto_version", "skip_verify",
		"keepalive", "dns_server", "ntp_server", "ntp_interval", "timezone",
		"disable_p2p", "p2p_type", "local_ip_forward", "auto_reconnect",
		"disconnect_timeout", "p2p_timeout",
	} {
		resetChanged(name)
	}
}

func npcBool(v bool) *bool { return &v }

func npcInt(v int) *int { return &v }

func npcString(v string) *string { return &v }

func setNPCFlagChanged(t *testing.T, name string, changed bool) {
	t.Helper()
	f := flag.CommandLine.Lookup(name)
	if f == nil {
		t.Fatalf("flag %q not found", name)
	}
	f.Changed = changed
}
