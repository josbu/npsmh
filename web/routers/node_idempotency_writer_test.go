package routers

import (
	"errors"
	"net"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNodeIdempotencyCaptureWriterHandlesNilState(t *testing.T) {
	var nilWriter *nodeIdempotencyCaptureWriter
	if _, err := nilWriter.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Write() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := nilWriter.WriteString("x"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil WriteString() error = %v, want %v", err, net.ErrClosed)
	}

	malformed := &nodeIdempotencyCaptureWriter{}
	if _, err := malformed.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed Write() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := malformed.WriteString("x"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed WriteString() error = %v, want %v", err, net.ErrClosed)
	}
}

func TestNodeIdempotencyCaptureWriterCachesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	writer := &nodeIdempotencyCaptureWriter{ResponseWriter: ctx.Writer}

	n, err := writer.Write([]byte("ab"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("Write() = %d, want 2", n)
	}
	n, err = writer.WriteString("cd")
	if err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("WriteString() = %d, want 2", n)
	}
	if got := writer.body.String(); got != "abcd" {
		t.Fatalf("cached body = %q, want %q", got, "abcd")
	}
	if got := rec.Body.String(); got != "abcd" {
		t.Fatalf("response body = %q, want %q", got, "abcd")
	}
}
