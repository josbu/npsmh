package p2p

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
)

func TestRunProviderSessionRejectsWrongStartRole(t *testing.T) {
	_, _, _, _, _, _, _, _, err := RunProviderSession(context.Background(), nil, P2PPunchStart{
		SessionID: "session-provider",
		Token:     "token-provider",
		Role:      common.WORK_P2P_VISITOR,
		PeerRole:  common.WORK_P2P_PROVIDER,
	}, "", "", "")
	if !errors.Is(err, ErrUnexpectedP2PControlPayload) {
		t.Fatalf("RunProviderSession() error = %v, want %v", err, ErrUnexpectedP2PControlPayload)
	}
}

func TestRunVisitorSessionRejectsWrongStartRole(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = WriteBridgeMessage(conn.NewConn(server), common.P2P_PUNCH_START, P2PPunchStart{
			SessionID: "session-visitor",
			Token:     "token-visitor",
			Role:      common.WORK_P2P_PROVIDER,
			PeerRole:  common.WORK_P2P_VISITOR,
		})
	}()

	_, _, _, _, _, _, _, _, err := RunVisitorSession(context.Background(), conn.NewConn(client), "", "", "")
	<-done
	if !errors.Is(err, ErrUnexpectedP2PControlPayload) {
		t.Fatalf("RunVisitorSession() error = %v, want %v", err, ErrUnexpectedP2PControlPayload)
	}
}

func TestNormalizeRuntimeSessionContextHandlesNil(t *testing.T) {
	if got := normalizeRuntimeSessionContext(nil); got == nil {
		t.Fatal("normalizeRuntimeSessionContext(nil) = nil, want background context")
	}
	ctx := context.Background()
	if got := normalizeRuntimeSessionContext(ctx); got != ctx {
		t.Fatalf("normalizeRuntimeSessionContext(ctx) = %#v, want same context", got)
	}
}

func TestAwaitPunchGoRejectsMismatchedSessionOrRole(t *testing.T) {
	tests := []struct {
		name  string
		goMsg P2PPunchGo
	}{
		{
			name: "session",
			goMsg: P2PPunchGo{
				SessionID: "other-session",
				Role:      common.WORK_P2P_VISITOR,
				DelayMs:   10,
			},
		},
		{
			name: "role",
			goMsg: P2PPunchGo{
				SessionID: "session-go",
				Role:      common.WORK_P2P_PROVIDER,
				DelayMs:   10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer func() { _ = server.Close() }()
			defer func() { _ = client.Close() }()

			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = WriteBridgeMessage(conn.NewConn(server), common.P2P_PUNCH_GO, tt.goMsg)
			}()

			err := awaitPunchGo(context.Background(), conn.NewConn(client), P2PPunchStart{
				SessionID: "session-go",
				Token:     "token-go",
				Role:      common.WORK_P2P_VISITOR,
				PeerRole:  common.WORK_P2P_PROVIDER,
			}, "")
			<-done
			if !errors.Is(err, ErrUnexpectedP2PControlPayload) {
				t.Fatalf("awaitPunchGo() error = %v, want %v", err, ErrUnexpectedP2PControlPayload)
			}
		})
	}
}

func TestValidateRuntimeSummaryRejectsMismatchedPayload(t *testing.T) {
	start := P2PPunchStart{
		SessionID: "session-summary",
		Token:     "token-summary",
		Role:      common.WORK_P2P_VISITOR,
		PeerRole:  common.WORK_P2P_PROVIDER,
	}
	summary := P2PProbeSummary{
		SessionID: "session-summary",
		Token:     "other-token",
		Role:      common.WORK_P2P_VISITOR,
		PeerRole:  common.WORK_P2P_PROVIDER,
	}
	if err := validateRuntimeSummary(start, summary); !errors.Is(err, ErrUnexpectedP2PControlPayload) {
		t.Fatalf("validateRuntimeSummary() error = %v, want %v", err, ErrUnexpectedP2PControlPayload)
	}
}
