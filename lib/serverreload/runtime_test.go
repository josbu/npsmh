package serverreload

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/web/routers"
)

func expectedResolvedLogPath(path string) string {
	if common.IsWindows() {
		return strings.ReplaceAll(path, "\\", "\\\\")
	}
	return path
}

func TestResolveLogSettingsReturnsZeroValuesForNilConfig(t *testing.T) {
	if got := ResolveLogSettings(nil); got != (LogSettings{}) {
		t.Fatalf("ResolveLogSettings(nil) = %+v, want zero-value settings", got)
	}
}

func TestResolveLogSettingsForcesFileLoggingInServiceMode(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"nps", "service"}
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	settings := ResolveLogSettings(&servercfg.Snapshot{
		Log: servercfg.LogConfig{
			Type:  "stdout",
			Level: "info",
		},
	})
	if settings.Type != "file" {
		t.Fatalf("ResolveLogSettings() type = %q, want file", settings.Type)
	}
}

func TestResolveLogSettingsPreservesBothLoggingInServiceMode(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"nps", "service"}
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	settings := ResolveLogSettings(&servercfg.Snapshot{
		Log: servercfg.LogConfig{
			Type:  "both",
			Level: "info",
		},
	})
	if settings.Type != "both" {
		t.Fatalf("ResolveLogSettings() type = %q, want both", settings.Type)
	}
}

func TestResolveLogSettingsUsesDefaultLogPathWhenEnabledWithoutExplicitPath(t *testing.T) {
	settings := ResolveLogSettings(&servercfg.Snapshot{
		Log: servercfg.LogConfig{
			Type:  "stdout",
			Level: "info",
			Path:  "true",
		},
	})

	wantPath := expectedResolvedLogPath(common.GetLogPath())
	if settings.Path != wantPath {
		t.Fatalf("ResolveLogSettings() path = %q, want %q", settings.Path, wantPath)
	}
}

func TestResolveLogSettingsNormalizesRelativePath(t *testing.T) {
	settings := ResolveLogSettings(&servercfg.Snapshot{
		Log: servercfg.LogConfig{
			Type:  "file",
			Level: "debug",
			Path:  filepath.Join("logs", "nps.log"),
		},
	})

	wantPath := expectedResolvedLogPath(filepath.Join(common.GetRunPath(), "logs", "nps.log"))
	if settings.Path != wantPath {
		t.Fatalf("ResolveLogSettings() path = %q, want %q", settings.Path, wantPath)
	}
}

func TestResolveLogSettingsLeavesSpecialPathsUntouched(t *testing.T) {
	testCases := []string{"off", "false", "docker", "/dev/null"}
	for _, path := range testCases {
		path := path
		t.Run(path, func(t *testing.T) {
			settings := ResolveLogSettings(&servercfg.Snapshot{
				Log: servercfg.LogConfig{
					Type:  "file",
					Level: "warn",
					Path:  path,
				},
			})
			if settings.Path != path {
				t.Fatalf("ResolveLogSettings() path = %q, want %q", settings.Path, path)
			}
		})
	}
}

func TestRunningAsService(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() {
		os.Args = originalArgs
	})

	os.Args = []string{"nps"}
	if runningAsService() {
		t.Fatal("runningAsService() = true, want false when no mode argument is provided")
	}

	os.Args = []string{"nps", " Service "}
	if !runningAsService() {
		t.Fatal("runningAsService() = false, want true for case-insensitive service mode")
	}
}

func TestWarnRestartRequiredChangesIncludesSharedMuxAndBridgeGateway(t *testing.T) {
	logs.EnableInMemoryBuffer(4096)
	logs.Init("off", "info", "", 1, 1, 1, false, false)

	previous := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Host: "web.old.example",
		},
		Network: servercfg.NetworkConfig{
			BridgeHost:         "bridge.old.example",
			BridgePath:         "/bridge",
			BridgeTrustedIPs:   "127.0.0.1",
			BridgeRealIPHeader: "X-Real-IP",
		},
	}
	current := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Host: "web.new.example",
		},
		Network: servercfg.NetworkConfig{
			BridgeHost:         "bridge.new.example",
			BridgePath:         "/bridge-v2",
			BridgeTrustedIPs:   "10.0.0.0/8",
			BridgeRealIPHeader: "X-Forwarded-For",
		},
	}

	warnRestartRequiredChanges(previous, current)
	output := logs.GetBufferedLogs()
	for _, want := range []string{"shared mux routing", "bridge websocket gateway"} {
		if !strings.Contains(output, want) {
			t.Fatalf("restart warning logs = %q, want substring %q", output, want)
		}
	}
}

func TestApplyManagedRuntimeSetsWebHandlerOnSuccess(t *testing.T) {
	oldReplaceManagedRuntime := replaceManagedRuntime
	oldSetWebHandler := setWebHandler
	defer func() {
		replaceManagedRuntime = oldReplaceManagedRuntime
		setWebHandler = oldSetWebHandler
	}()

	expectedHandler := http.NewServeMux()
	replaceManagedRuntime = func(_ *servercfg.Snapshot) *routers.Runtime {
		return &routers.Runtime{Handler: expectedHandler}
	}

	var gotHandler http.Handler
	setCalls := 0
	setWebHandler = func(handler http.Handler) {
		setCalls++
		gotHandler = handler
	}

	if err := applyManagedRuntime(); err != nil {
		t.Fatalf("applyManagedRuntime() error = %v, want nil", err)
	}
	if setCalls != 1 {
		t.Fatalf("setWebHandler call count = %d, want 1", setCalls)
	}
	if gotHandler == nil {
		t.Fatal("setWebHandler handler = nil, want expected handler")
	}
	if reflect.ValueOf(gotHandler).Pointer() != reflect.ValueOf(expectedHandler).Pointer() {
		t.Fatalf("setWebHandler handler = %p, want %p", gotHandler, expectedHandler)
	}
}

func TestApplyManagedRuntimeKeepsExistingWebHandlerOnFailure(t *testing.T) {
	oldReplaceManagedRuntime := replaceManagedRuntime
	oldSetWebHandler := setWebHandler
	defer func() {
		replaceManagedRuntime = oldReplaceManagedRuntime
		setWebHandler = oldSetWebHandler
	}()

	expectedErr := errors.New("session-store-failed")
	replaceManagedRuntime = func(_ *servercfg.Snapshot) *routers.Runtime {
		return &routers.Runtime{Err: expectedErr}
	}

	setCalls := 0
	setWebHandler = func(handler http.Handler) {
		setCalls++
	}

	err := applyManagedRuntime()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("applyManagedRuntime() error = %v, want %v", err, expectedErr)
	}
	if setCalls != 0 {
		t.Fatalf("setWebHandler call count = %d, want 0 on runtime failure", setCalls)
	}
}

func TestApplyManagedRuntimeRejectsNilRuntime(t *testing.T) {
	oldReplaceManagedRuntime := replaceManagedRuntime
	oldSetWebHandler := setWebHandler
	defer func() {
		replaceManagedRuntime = oldReplaceManagedRuntime
		setWebHandler = oldSetWebHandler
	}()

	replaceManagedRuntime = func(_ *servercfg.Snapshot) *routers.Runtime {
		return nil
	}

	setCalls := 0
	setWebHandler = func(handler http.Handler) {
		setCalls++
	}

	err := applyManagedRuntime()
	if !errors.Is(err, errManagedRuntimeUnavailable) {
		t.Fatalf("applyManagedRuntime() error = %v, want %v", err, errManagedRuntimeUnavailable)
	}
	if setCalls != 0 {
		t.Fatalf("setWebHandler call count = %d, want 0 for nil runtime", setCalls)
	}
}
