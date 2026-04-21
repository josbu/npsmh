//go:build !sdk

package main

import (
	"testing"

	"github.com/kardianos/service"
)

type stubNPCService struct{}

func (stubNPCService) Run() error                                  { return nil }
func (stubNPCService) Start() error                                { return nil }
func (stubNPCService) Stop() error                                 { return nil }
func (stubNPCService) Restart() error                              { return nil }
func (stubNPCService) Install() error                              { return nil }
func (stubNPCService) Uninstall() error                            { return nil }
func (stubNPCService) Logger(chan<- error) (service.Logger, error) { return nil, nil }
func (stubNPCService) SystemLogger(chan<- error) (service.Logger, error) {
	return nil, nil
}
func (stubNPCService) String() string                  { return "stub" }
func (stubNPCService) Platform() string                { return "test" }
func (stubNPCService) Status() (service.Status, error) { return service.StatusUnknown, nil }

func TestNPCSignalExitIsIdempotent(t *testing.T) {
	prg := &Npc{}
	prg.signalExit()
	prg.signalExit()

	select {
	case <-prg.exitChan():
	default:
		t.Fatal("signalExit() did not close exit channel")
	}
}

func TestNPCStopHandlesRepeatedCalls(t *testing.T) {
	oldInteractive := npcServiceInteractive
	npcServiceInteractive = func() bool { return false }
	t.Cleanup(func() {
		npcServiceInteractive = oldInteractive
	})

	prg := &Npc{}
	svc := stubNPCService{}

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
