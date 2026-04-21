package httpproxy

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
)

type httpCountingLimiter struct {
	getCalls    int64
	returnCalls int64
	bytes       int64
}

func (l *httpCountingLimiter) Get(size int64) {
	atomic.AddInt64(&l.getCalls, 1)
	atomic.AddInt64(&l.bytes, size)
}

func (l *httpCountingLimiter) ReturnBucket(size int64) {
	atomic.AddInt64(&l.returnCalls, 1)
	atomic.AddInt64(&l.bytes, -size)
}

type recordingResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
	limit  int
}

func (w *recordingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *recordingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *recordingResponseWriter) Write(p []byte) (int, error) {
	if w.limit > 0 && len(p) > w.limit {
		w.body.Write(p[:w.limit])
		return w.limit, nil
	}
	return w.body.Write(p)
}

func TestWrapReadCloserWithLimiterChargesBytes(t *testing.T) {
	limiter := &httpCountingLimiter{}
	body := wrapReadCloserWithLimiter(io.NopCloser(bytes.NewReader([]byte("hello"))), limiter, nil)

	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("body = %q, want hello", string(data))
	}
	if limiter.bytes != 5 || limiter.getCalls == 0 {
		t.Fatalf("limiter state bytes=%d getCalls=%d, want 5/>0", limiter.bytes, limiter.getCalls)
	}
}

func TestLimitedResponseWriterWriteRefundsShortWrite(t *testing.T) {
	limiter := &httpCountingLimiter{}
	base := &recordingResponseWriter{limit: 2}
	writer := wrapResponseWriterWithLimiter(base, limiter, nil)

	n, err := writer.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("Write() = %d, want 2", n)
	}
	if limiter.bytes != 2 || limiter.returnCalls != 1 {
		t.Fatalf("limiter state bytes=%d returnCalls=%d, want 2/1", limiter.bytes, limiter.returnCalls)
	}
}

func TestLimitedReaderRefundsLimiterOnObserverError(t *testing.T) {
	limiter := &httpCountingLimiter{}
	wantErr := errors.New("observer failed")
	reader := &limitedReader{
		reader:  bytes.NewReader([]byte("payload")),
		limiter: limiter,
		observer: func(int64) error {
			return wantErr
		},
	}

	n, err := reader.Read(make([]byte, len("payload")))
	if n != len("payload") {
		t.Fatalf("Read() n = %d, want %d", n, len("payload"))
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Read() error = %v, want %v", err, wantErr)
	}
	if limiter.bytes != 0 || limiter.getCalls != 1 || limiter.returnCalls != 1 {
		t.Fatalf("limiter state bytes=%d getCalls=%d returnCalls=%d, want 0/1/1", limiter.bytes, limiter.getCalls, limiter.returnCalls)
	}
}

func TestLimitedResponseWriterReadFromChargesBytes(t *testing.T) {
	limiter := &httpCountingLimiter{}
	base := &recordingResponseWriter{}
	writer := wrapResponseWriterWithLimiter(base, limiter, nil)
	readerFrom, ok := writer.(io.ReaderFrom)
	if !ok {
		t.Fatal("wrapped response writer should implement io.ReaderFrom")
	}

	n, err := readerFrom.ReadFrom(bytes.NewReader([]byte("stream")))
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if n != 6 {
		t.Fatalf("ReadFrom() copied %d bytes, want 6", n)
	}
	if got := base.body.String(); got != "stream" {
		t.Fatalf("body = %q, want stream", got)
	}
	if limiter.bytes != 6 || limiter.getCalls == 0 {
		t.Fatalf("limiter state bytes=%d getCalls=%d, want 6/>0", limiter.bytes, limiter.getCalls)
	}
}

func TestLimitedWrappersHandleNilState(t *testing.T) {
	var nilReader *limitedReader
	if _, err := nilReader.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil limitedReader.Read() error = %v, want %v", err, net.ErrClosed)
	}

	var nilCloser *limitedReadCloser
	if _, err := nilCloser.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil limitedReadCloser.Read() error = %v, want %v", err, net.ErrClosed)
	}
	if err := nilCloser.Close(); err != nil {
		t.Fatalf("nil limitedReadCloser.Close() error = %v, want nil", err)
	}

	malformedReader := &limitedReader{}
	if _, err := malformedReader.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed limitedReader.Read() error = %v, want %v", err, net.ErrClosed)
	}

	malformedCloser := &limitedReadCloser{}
	if _, err := malformedCloser.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed limitedReadCloser.Read() error = %v, want %v", err, net.ErrClosed)
	}
	if err := malformedCloser.Close(); err != nil {
		t.Fatalf("malformed limitedReadCloser.Close() error = %v, want nil", err)
	}

	var nilWriter *limitedResponseWriter
	if _, err := nilWriter.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil limitedResponseWriter.Write() error = %v, want %v", err, net.ErrClosed)
	}
	nilWriter.Flush()
	if err := nilWriter.Push("/x", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("nil limitedResponseWriter.Push() error = %v, want %v", err, http.ErrNotSupported)
	}

	malformedWriter := &limitedResponseWriter{}
	if _, err := malformedWriter.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed limitedResponseWriter.Write() error = %v, want %v", err, net.ErrClosed)
	}
	malformedWriter.Flush()
	if err := malformedWriter.Push("/x", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("malformed limitedResponseWriter.Push() error = %v, want %v", err, http.ErrNotSupported)
	}
	if _, err := malformedWriter.ReadFrom(nil); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed limitedResponseWriter.ReadFrom() error = %v, want %v", err, net.ErrClosed)
	}

	if _, err := (responseWriterOnly{}).Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("responseWriterOnly{}.Write() error = %v, want %v", err, net.ErrClosed)
	}
}

func TestLimitedWrappersReturnOriginalWhenLimitingDisabled(t *testing.T) {
	reader := io.NopCloser(bytes.NewReader([]byte("payload")))
	if got := wrapReadCloserWithLimiter(reader, nil, nil); got != reader {
		t.Fatalf("wrapReadCloserWithLimiter() = %#v, want original reader %#v", got, reader)
	}

	writer := &recordingResponseWriter{}
	if got := wrapResponseWriterWithLimiter(writer, nil, nil); got != writer {
		t.Fatalf("wrapResponseWriterWithLimiter() = %#v, want original writer %#v", got, writer)
	}
}
