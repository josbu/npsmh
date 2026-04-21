package routers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/gorilla/websocket"
)

func TestReplaceManagedRuntimeReconnectsReversePlatformsAfterReload(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	StopManagedRuntime()
	t.Cleanup(StopManagedRuntime)

	newReversePlatformServer := func(path string) (*httptest.Server, <-chan nodeWSFrame, <-chan *websocket.Conn) {
		helloCh := make(chan nodeWSFrame, 1)
		connCh := make(chan *websocket.Conn, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("Upgrade(%s) error = %v", path, err)
				return
			}
			var hello nodeWSFrame
			if err := conn.ReadJSON(&hello); err != nil {
				_ = conn.Close()
				t.Errorf("ReadJSON(hello %s) error = %v", path, err)
				return
			}
			helloCh <- hello
			connCh <- conn
		}))
		return srv, helloCh, connCh
	}

	platformASrv, platformAHello, platformAConn := newReversePlatformServer("/reverse/a")
	defer platformASrv.Close()
	platformBSrv, platformBHello, platformBConn := newReversePlatformServer("/reverse/b")
	defer platformBSrv.Close()

	reverseAURL := "ws" + strings.TrimPrefix(platformASrv.URL, "http") + "/reverse/a"
	reverseBURL := "ws" + strings.TrimPrefix(platformBSrv.URL, "http") + "/reverse/b"
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-a",
		"platform_tokens=token-a",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_a",
		"platform_connect_modes=reverse",
		"platform_reverse_ws_urls=" + reverseAURL,
		"platform_reverse_enabled=true",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	managedRuntime := ReplaceManagedRuntime(nil)
	if managedRuntime == nil || managedRuntime.Handler == nil {
		t.Fatal("ReplaceManagedRuntime() returned nil runtime")
	}

	var helloA nodeWSFrame
	select {
	case helloA = <-platformAHello:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reverse hello from platform A")
	}
	if helloA.Type != "hello" || !strings.Contains(string(helloA.Body), "\"platform_id\":\"master-a\"") {
		t.Fatalf("unexpected reverse hello for platform A: %+v body=%s", helloA, string(helloA.Body))
	}
	select {
	case conn := <-platformAConn:
		defer func() { _ = conn.Close() }()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for platform A websocket connection")
	}

	reloadedConfig := strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-b",
		"platform_tokens=token-b",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_b",
		"platform_connect_modes=reverse",
		"platform_reverse_ws_urls=" + reverseBURL,
		"platform_reverse_enabled=true",
		"web_username=admin",
		"web_password=secret",
	}, "\n") + "\n"
	if err := os.WriteFile(configPath, []byte(reloadedConfig), 0o600); err != nil {
		t.Fatalf("WriteFile(reload config) error = %v", err)
	}
	if err := servercfg.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}

	reloadedRuntime := ReplaceManagedRuntime(nil)
	if reloadedRuntime == nil || reloadedRuntime.Handler == nil {
		t.Fatal("ReplaceManagedRuntime() after Reload() returned nil runtime")
	}

	var helloB nodeWSFrame
	select {
	case helloB = <-platformBHello:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reverse hello from platform B after reload")
	}
	if helloB.Type != "hello" || !strings.Contains(string(helloB.Body), "\"platform_id\":\"master-b\"") {
		t.Fatalf("unexpected reverse hello for platform B: %+v body=%s", helloB, string(helloB.Body))
	}
	select {
	case conn := <-platformBConn:
		defer func() { _ = conn.Close() }()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for platform B websocket connection")
	}
}
