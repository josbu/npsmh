package p2p

import (
	"context"
	"testing"
)

func TestAwaitRuntimeConfirmationPrefersConfirmedPairAfterContextDone(t *testing.T) {
	confirmed := make(chan confirmedPair, 1)
	want := confirmedPair{localAddr: "127.0.0.1:3000", remoteAddr: "198.51.100.10:4000"}
	confirmed <- want

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	workers := []*runtimeFamilyWorker{
		{
			rs: &runtimeSession{confirmed: confirmed},
		},
	}

	got, ok := awaitRuntimeConfirmation(ctx, workers)
	if !ok {
		t.Fatal("awaitRuntimeConfirmation() = not confirmed, want queued confirmed pair")
	}
	if got.localAddr != want.localAddr || got.remoteAddr != want.remoteAddr {
		t.Fatalf("awaitRuntimeConfirmation() = %#v, want %#v", got, want)
	}
}

func TestAwaitRuntimeConfirmationReturnsFalseWithoutQueuedPair(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	workers := []*runtimeFamilyWorker{
		{
			rs: &runtimeSession{confirmed: make(chan confirmedPair, 1)},
		},
	}

	if _, ok := awaitRuntimeConfirmation(ctx, workers); ok {
		t.Fatal("awaitRuntimeConfirmation() = confirmed, want false when context is done and no pair is queued")
	}
}
