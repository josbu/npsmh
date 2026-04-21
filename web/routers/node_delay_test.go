package routers

import (
	"context"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

type delayTestManagementPlatformStore struct {
	serviceUser *file.User
}

type delayTestRuntimeStatusStore struct {
	base         *webservice.InMemoryManagementPlatformRuntimeStatusStore
	disconnected chan struct{}
}

func (s delayTestManagementPlatformStore) EnsureServiceUser(servercfg.ManagementPlatformConfig) (*file.User, error) {
	if s.serviceUser != nil {
		return s.serviceUser, nil
	}
	return &file.User{Id: 1}, nil
}

func (delayTestManagementPlatformStore) ServiceUsername(platformID, configured string) string {
	if configured != "" {
		return configured
	}
	return platformID
}

func (delayTestManagementPlatformStore) OwnedClientIDs(int) ([]int, error) {
	return nil, nil
}

func (s *delayTestRuntimeStatusStore) Reset() {
	s.base.Reset()
}

func (s *delayTestRuntimeStatusStore) Status(platformID string) webservice.ManagementPlatformReverseRuntimeStatus {
	return s.base.Status(platformID)
}

func (s *delayTestRuntimeStatusStore) NoteConfigured(platformID, connectMode, reverseWSURL string, reverseEnabled bool) {
	s.base.NoteConfigured(platformID, connectMode, reverseWSURL, reverseEnabled)
}

func (s *delayTestRuntimeStatusStore) NoteCallbackConfigured(platformID, callbackURL string, callbackEnabled bool, callbackTimeoutSeconds int, callbackRetryMax int, callbackRetryBackoffSec int, callbackQueueMax int, callbackQueueSize int) {
	s.base.NoteCallbackConfigured(platformID, callbackURL, callbackEnabled, callbackTimeoutSeconds, callbackRetryMax, callbackRetryBackoffSec, callbackQueueMax, callbackQueueSize)
}

func (s *delayTestRuntimeStatusStore) NoteReverseConnected(platformID string) {
	s.base.NoteReverseConnected(platformID)
}

func (s *delayTestRuntimeStatusStore) NoteReverseDisconnected(platformID string, err error) {
	s.base.NoteReverseDisconnected(platformID, err)
	if s != nil && s.disconnected != nil {
		select {
		case s.disconnected <- struct{}{}:
		default:
		}
	}
}

func (s *delayTestRuntimeStatusStore) NoteReverseHello(platformID string) {
	s.base.NoteReverseHello(platformID)
}

func (s *delayTestRuntimeStatusStore) NoteReversePing(platformID string) {
	s.base.NoteReversePing(platformID)
}

func (s *delayTestRuntimeStatusStore) NoteReversePong(platformID string) {
	s.base.NoteReversePong(platformID)
}

func (s *delayTestRuntimeStatusStore) NoteReverseEvent(platformID string) {
	s.base.NoteReverseEvent(platformID)
}

func (s *delayTestRuntimeStatusStore) NoteCallbackDelivered(platformID string, statusCode int) {
	s.base.NoteCallbackDelivered(platformID, statusCode)
}

func (s *delayTestRuntimeStatusStore) NoteCallbackQueued(platformID string, queueSize int, dropped bool) {
	s.base.NoteCallbackQueued(platformID, queueSize, dropped)
}

func (s *delayTestRuntimeStatusStore) NoteCallbackReplayDelivered(platformID string, statusCode int, queueSize int) {
	s.base.NoteCallbackReplayDelivered(platformID, statusCode, queueSize)
}

func (s *delayTestRuntimeStatusStore) NoteCallbackQueueSize(platformID string, queueSize int) {
	s.base.NoteCallbackQueueSize(platformID, queueSize)
}

func (s *delayTestRuntimeStatusStore) NoteCallbackFailed(platformID string, err error, statusCode int) {
	s.base.NoteCallbackFailed(platformID, err, statusCode)
}

func TestWaitNodeDelayReturnsFalseWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	if ok := waitNodeDelay(ctx, time.Second); ok {
		t.Fatal("waitNodeDelay() = true, want false for canceled context")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("waitNodeDelay() took %v after cancellation, want prompt return", elapsed)
	}
}

func TestNodeReverseRetryDelayBackoffPolicy(t *testing.T) {
	tests := []struct {
		name         string
		backoff      time.Duration
		connected    bool
		wantDelay    time.Duration
		wantNextBack time.Duration
	}{
		{
			name:         "initial failure",
			backoff:      0,
			wantDelay:    time.Second,
			wantNextBack: 2 * time.Second,
		},
		{
			name:         "failure doubles up to max",
			backoff:      8 * time.Second,
			wantDelay:    8 * time.Second,
			wantNextBack: 15 * time.Second,
		},
		{
			name:         "failure stays capped",
			backoff:      15 * time.Second,
			wantDelay:    15 * time.Second,
			wantNextBack: 15 * time.Second,
		},
		{
			name:         "successful connect resets delay",
			backoff:      15 * time.Second,
			connected:    true,
			wantDelay:    time.Second,
			wantNextBack: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDelay, gotNextBackoff := nodeReverseRetryDelay(tt.backoff, tt.connected)
			if gotDelay != tt.wantDelay || gotNextBackoff != tt.wantNextBack {
				t.Fatalf("nodeReverseRetryDelay(%v, %v) = (%v, %v), want (%v, %v)", tt.backoff, tt.connected, gotDelay, gotNextBackoff, tt.wantDelay, tt.wantNextBack)
			}
		})
	}
}

func TestNodeReverseManagerRunPlatformStopsPromptlyDuringBackoff(t *testing.T) {
	app := webapi.New(nil)
	app.Services.ManagementPlatforms = delayTestManagementPlatformStore{serviceUser: &file.User{Id: 7}}
	runtimeStatus := &delayTestRuntimeStatusStore{
		base:         webservice.NewInMemoryManagementPlatformRuntimeStatusStore(),
		disconnected: make(chan struct{}, 1),
	}
	app.Services.ManagementPlatformRuntimeStatus = runtimeStatus
	state := NewStateWithApp(app)
	defer state.Close()
	ensureNodeManagementPlatformRuntimeStore(state)

	manager := newNodeReverseManager(state)
	platform := servercfg.ManagementPlatformConfig{
		PlatformID:     "platform-a",
		Enabled:        true,
		ReverseEnabled: true,
		ReverseWSURL:   "://invalid-reverse-url",
	}

	done := make(chan struct{})
	manager.wg.Add(1)
	go func() {
		manager.runPlatform(platform)
		close(done)
	}()

	select {
	case <-runtimeStatus.disconnected:
	case <-time.After(time.Second):
		t.Fatal("reverse manager did not enter disconnected backoff state")
	}

	manager.cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("runPlatform() should stop promptly when canceled during backoff")
	}
}
