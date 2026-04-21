package routers

import (
	"errors"
	"testing"

	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

func TestDeliverNodeReverseEventClosesConnectionOnWriteError(t *testing.T) {
	status := webservice.NewInMemoryManagementPlatformRuntimeStatusStore()
	var closed bool

	ok := deliverNodeReverseEvent(
		"platform-a",
		status,
		webapi.Event{Name: "client.updated", Sequence: 7},
		func(nodeWSFrame) error { return errors.New("write failed") },
		func() { closed = true },
	)

	if ok {
		t.Fatal("deliverNodeReverseEvent() should fail when writeFrame returns an error")
	}
	if !closed {
		t.Fatal("deliverNodeReverseEvent() should close the connection on write failure")
	}
	if payload := status.Status("platform-a"); payload.LastEventAt != 0 {
		t.Fatalf("deliverNodeReverseEvent() should not mark reverse event success on write failure, got %+v", payload)
	}
}

func TestDeliverNodeReverseEventUpdatesRuntimeStatusOnSuccess(t *testing.T) {
	status := webservice.NewInMemoryManagementPlatformRuntimeStatusStore()
	var frames []nodeWSFrame

	ok := deliverNodeReverseEvent(
		"platform-a",
		status,
		webapi.Event{Name: "client.updated", Sequence: 7},
		func(frame nodeWSFrame) error {
			frames = append(frames, frame)
			return nil
		},
		nil,
	)

	if !ok {
		t.Fatal("deliverNodeReverseEvent() should succeed when writeFrame succeeds")
	}
	if len(frames) != 1 || frames[0].Type != "event" {
		t.Fatalf("deliverNodeReverseEvent() frames = %+v, want one event frame", frames)
	}
	if payload := status.Status("platform-a"); payload.LastEventAt == 0 {
		t.Fatalf("deliverNodeReverseEvent() should mark reverse event success, got %+v", payload)
	}
}
