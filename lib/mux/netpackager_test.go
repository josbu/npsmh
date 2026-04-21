package mux

import (
	"bytes"
	"strings"
	"testing"
)

func TestBasePackagerSetRejectsInvalidContent(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{name: "nil", content: nil},
		{name: "empty", content: []byte{}},
		{name: "oversized", content: bytes.Repeat([]byte("x"), maximumSegmentSize+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pack basePackager
			if err := pack.Set(tt.content); err == nil {
				t.Fatal("Set() error = nil, want rejection for invalid content")
			}
		})
	}
}

func TestMuxPackagerSetRejectsNilDataContent(t *testing.T) {
	var pack muxPackager
	if err := pack.Set(muxNewMsg, 1, nil); err == nil {
		t.Fatal("Set() error = nil, want rejection for nil data content")
	}
}

func TestMuxPackagerSetRejectsUnexpectedContentTypes(t *testing.T) {
	tests := []struct {
		name    string
		flag    uint8
		content interface{}
		want    string
	}{
		{name: "data frame", flag: muxNewMsg, content: "bad", want: "want []byte"},
		{name: "window update", flag: muxMsgSendOk, content: "bad", want: "want uint64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pack muxPackager
			err := pack.Set(tt.flag, 1, tt.content)
			if err == nil {
				t.Fatal("Set() error = nil, want type rejection")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Set() error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestNormalizeMuxConfigClampsPingMaxPad(t *testing.T) {
	cfg := normalizeMuxConfig(MuxConfig{
		PingMaxPad:              maximumSegmentSize + 128,
		MaxConnReceiveWindow:    defaultInitialConnWindow,
		MaxSessionReceiveWindow: uint64(defaultInitialConnWindow),
	})

	if got, want := cfg.PingMaxPad, maximumSegmentSize-8; got != want {
		t.Fatalf("PingMaxPad = %d, want %d", got, want)
	}
}
