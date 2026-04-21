package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/kardianos/service"
)

type stubNPSService struct{}

func (stubNPSService) Run() error       { return nil }
func (stubNPSService) Start() error     { return nil }
func (stubNPSService) Stop() error      { return nil }
func (stubNPSService) Restart() error   { return nil }
func (stubNPSService) Install() error   { return nil }
func (stubNPSService) Uninstall() error { return nil }
func (stubNPSService) Logger(chan<- error) (service.Logger, error) {
	return nil, nil
}
func (stubNPSService) SystemLogger(chan<- error) (service.Logger, error) {
	return nil, nil
}
func (stubNPSService) String() string                  { return "stub" }
func (stubNPSService) Platform() string                { return "test" }
func (stubNPSService) Status() (service.Status, error) { return service.StatusUnknown, nil }

func TestLoadStartupConfigReturnsErrorForMissingConfig(t *testing.T) {
	oldConfPath := common.ConfPath
	servercfg.SetPreferredPath("")
	t.Cleanup(func() {
		common.ConfPath = oldConfPath
		servercfg.SetPreferredPath("")
	})

	missing := filepath.Join(t.TempDir(), "missing.json")
	if _, err := loadStartupConfig(missing); err == nil {
		t.Fatal("loadStartupConfig() error = nil, want missing config error")
	}
}

func TestLoadStartupConfigAppliesExplicitFilePath(t *testing.T) {
	oldConfPath := common.ConfPath
	servercfg.SetPreferredPath("")
	t.Cleanup(func() {
		common.ConfPath = oldConfPath
		servercfg.SetPreferredPath("")
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "nps.json")
	if err := os.WriteFile(path, []byte(`{"log":{"level":"trace"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	startup, err := loadStartupConfig(path)
	if err != nil {
		t.Fatalf("loadStartupConfig() error = %v", err)
	}
	if got := servercfg.Path(); got != path {
		t.Fatalf("servercfg.Path() = %q, want %q", got, path)
	}
	if got := common.ConfPath; got != dir {
		t.Fatalf("common.ConfPath = %q, want %q", got, dir)
	}
	if startup.Config == nil {
		t.Fatal("loadStartupConfig() returned nil config")
	}
	if startup.LogSettings.Level != "trace" {
		t.Fatalf("loadStartupConfig() log level = %q, want %q", startup.LogSettings.Level, "trace")
	}
}

func TestNPSSignalExitIsIdempotent(t *testing.T) {
	prg := &nps{}
	prg.signalExit()
	prg.signalExit()

	select {
	case <-prg.exitChan():
	default:
		t.Fatal("signalExit() did not close exit channel")
	}
}

func TestNPSStopHandlesRepeatedCalls(t *testing.T) {
	oldInteractive := npsServiceInteractive
	npsServiceInteractive = func() bool { return false }
	t.Cleanup(func() {
		npsServiceInteractive = oldInteractive
	})

	prg := &nps{}
	svc := stubNPSService{}

	if err := prg.Stop(svc); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := prg.Stop(svc); err != nil {
		t.Fatalf("Stop() second call error = %v", err)
	}

	select {
	case <-prg.exitChan():
	default:
		t.Fatal("Stop() did not close exit channel")
	}
}
