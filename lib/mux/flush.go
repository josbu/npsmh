package mux

import (
	"io"
	"net"
	"time"
)

// FlushWriter buffers small writes before flushing them to conn.
//
// Deprecated: kept as a low-level helper for compatibility. Most callers should
// write through Mux or Conn instead.
type FlushWriter struct {
	conn         net.Conn
	buf          []byte
	maxSize      int
	err          error
	writeTimeout time.Duration
}

// NewFlushWriter creates a FlushWriter without per-flush write deadlines.
func NewFlushWriter(conn net.Conn) *FlushWriter {
	return NewFlushWriterWithTimeout(conn, 0)
}

// NewFlushWriterWithTimeout creates a FlushWriter that applies a write deadline
// around each flush.
func NewFlushWriterWithTimeout(conn net.Conn, writeTimeout time.Duration) *FlushWriter {
	buf := make([]byte, 0, poolSizeWindowBuffer)
	return &FlushWriter{
		conn:         conn,
		buf:          buf,
		maxSize:      cap(buf),
		writeTimeout: writeTimeout,
	}
}

func (w *FlushWriter) Write(p []byte) (n int, err error) {
	if w.err != nil {
		return 0, w.err
	}
	n = len(p)
	b := len(w.buf)
	t := n + b
	if t > w.maxSize && b > 0 {
		if err := w.flush(); err != nil {
			return 0, err
		}
	}
	if n > w.maxSize {
		_, err := w.writeAll(p)
		return n, err
	}
	w.buf = append(w.buf, p...)
	return n, nil
}

func (w *FlushWriter) Flush() error {
	if w.err != nil {
		return w.err
	}
	return w.flush()
}

func (w *FlushWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	_, err := w.writeAll(w.buf)
	w.buf = w.buf[:0]
	return err
}

func (w *FlushWriter) Close() error {
	err := w.err
	if err == nil && len(w.buf) > 0 {
		err = w.flush()
	}
	w.buf = nil
	return err
}

func (w *FlushWriter) setWriteDeadline(deadline time.Time) error {
	if w.conn == nil {
		return net.ErrClosed
	}
	return w.conn.SetWriteDeadline(deadline)
}

func (w *FlushWriter) writeAll(p []byte) (n int, err error) {
	if w.writeTimeout > 0 {
		if err := w.setWriteDeadline(time.Now().Add(w.writeTimeout)); err != nil {
			w.err = err
			return 0, err
		}
		defer func() {
			if clearErr := w.setWriteDeadline(time.Time{}); err == nil && clearErr != nil {
				err = clearErr
				w.err = clearErr
			}
		}()
	}
	for n < len(p) {
		var nn int
		nn, err = w.conn.Write(p[n:])
		n += nn
		if err != nil {
			w.err = err
			return n, err
		}
		if nn == 0 {
			w.err = io.ErrShortWrite
			return n, w.err
		}
	}
	return n, nil
}
