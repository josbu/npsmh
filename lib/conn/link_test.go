package conn

import (
	"net"
	"testing"
	"time"
)

func TestLinkTimeoutNormalizesNonPositiveValues(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "positive", timeout: 2 * time.Second, want: 2 * time.Second},
		{name: "zero", timeout: 0, want: defaultTimeOut},
		{name: "negative", timeout: -time.Second, want: defaultTimeOut},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link := NewLink("tcp", "127.0.0.1:80", false, false, "", false, LinkTimeout(tt.timeout))
			if got := link.Option.Timeout; got != tt.want {
				t.Fatalf("NewLink(..., LinkTimeout(%v)).Option.Timeout = %v, want %v", tt.timeout, got, tt.want)
			}
		})
	}
}

func TestGetLinkInfoNormalizesNonPositiveTimeoutFromWire(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	errCh := make(chan error, 1)
	go func() {
		link := &Link{
			ConnType: "tcp",
			Host:     "127.0.0.1:443",
			Option: Options{
				Timeout:           0,
				NeedAck:           true,
				WaitConnectResult: true,
			},
		}
		_, err := NewConn(server).SendInfo(link, "")
		errCh <- err
	}()

	got, err := NewConn(client).GetLinkInfo()
	if err != nil {
		t.Fatalf("GetLinkInfo() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetLinkInfo() = nil, want link")
	}
	if got.Option.Timeout != defaultTimeOut {
		t.Fatalf("GetLinkInfo().Option.Timeout = %v, want %v", got.Option.Timeout, defaultTimeOut)
	}
	if !got.Option.NeedAck || !got.Option.WaitConnectResult {
		t.Fatalf("GetLinkInfo() lost option flags: %+v", got.Option)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("SendInfo() error = %v", err)
	}
}
