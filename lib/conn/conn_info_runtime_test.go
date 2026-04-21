package conn

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

type connInfoReader struct {
	name string
	read func(*Conn) error
}

func TestConnInfoReadersRejectMalformedPayloadWithoutPanic(t *testing.T) {
	for _, reader := range connInfoReaders() {
		t.Run(reader.name, func(t *testing.T) {
			err, panicValue := runConnInfoReaderPayload([]byte("{"), reader.read)
			if panicValue != nil {
				t.Fatalf("reader panicked: %v", panicValue)
			}
			if err == nil {
				t.Fatal("reader error = nil, want malformed payload error")
			}
		})
	}
}

func TestConnInfoReadersRejectNullPayloadWithoutPanic(t *testing.T) {
	for _, reader := range connInfoReaders() {
		t.Run(reader.name, func(t *testing.T) {
			err, panicValue := runConnInfoReaderPayload([]byte("null"), reader.read)
			if panicValue != nil {
				t.Fatalf("reader panicked: %v", panicValue)
			}
			if err == nil {
				t.Fatal("reader error = nil, want nil payload error")
			}
			if !strings.Contains(err.Error(), "receive") {
				t.Fatalf("reader error = %v, want typed receive error", err)
			}
		})
	}
}

func connInfoReaders() []connInfoReader {
	return []connInfoReader{
		{
			name: "client",
			read: func(c *Conn) error {
				_, err := c.GetConfigInfo()
				return err
			},
		},
		{
			name: "host",
			read: func(c *Conn) error {
				_, err := c.GetHostInfo()
				return err
			},
		},
		{
			name: "task",
			read: func(c *Conn) error {
				_, err := c.GetTaskInfo()
				return err
			},
		},
	}
}

func runConnInfoReaderPayload(payload []byte, read func(*Conn) error) (err error, panicValue any) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	writeErrCh := make(chan error, 1)
	go func() {
		defer close(writeErrCh)
		writeErrCh <- writeFramedConnInfoPayload(server, payload)
	}()

	func() {
		defer func() { panicValue = recover() }()
		err = read(NewConn(client))
	}()

	if writeErr := <-writeErrCh; writeErr != nil {
		return writeErr, panicValue
	}
	return err, panicValue
}

func writeFramedConnInfoPayload(conn net.Conn, payload []byte) error {
	defer func() { _ = conn.Close() }()
	if err := binary.Write(conn, binary.LittleEndian, int32(len(payload))); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}
