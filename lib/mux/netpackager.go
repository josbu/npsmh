package mux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type basePackager struct {
	head    [13]byte
	length  uint16
	content []byte
}

func (Self *basePackager) Set(content []byte) (err error) {
	Self.reset()

	if content == nil {
		return errors.New("mux:packer: new pack content is nil")
	}

	n := len(content)
	if n == 0 {
		return errors.New("mux:packer: new pack content is empty")
	}
	if n > maximumSegmentSize {
		return errors.New("mux:packer: new pack content segment too large")
	}

	if Self.content == nil {
		Self.content = windowBuff.Get()
	}
	if cap(Self.content) < n {
		return errors.New("mux:packer: buf too small")
	}
	copy(Self.content[:n], content)
	Self.length = uint16(n)
	return
}

func (Self *basePackager) GetContent() (content []byte, err error) {
	if Self.length == 0 || Self.content == nil {
		return nil, errors.New("mux:packer:content is nil")
	}
	return Self.content[:Self.length], nil
}

func (Self *basePackager) Pack(writer io.Writer) (err error) {
	binary.LittleEndian.PutUint16(Self.head[5:7], Self.length)
	_, err = writer.Write(Self.head[:7])
	if err != nil {
		return
	}
	_, err = writer.Write(Self.content[:Self.length])
	return
}

func (Self *basePackager) UnPack(reader io.Reader) (n uint16, err error) {
	Self.reset()
	l, err := io.ReadFull(reader, Self.head[5:7])
	if err != nil {
		return
	}
	n += uint16(l)
	Self.length = binary.LittleEndian.Uint16(Self.head[5:7])

	if int(Self.length) > maximumSegmentSize {
		err = errors.New("mux:packer: unpack content segment too large")
		return
	}

	if Self.content == nil {
		Self.content = windowBuff.Get()
	}
	if int(Self.length) > cap(Self.content) {
		err = errors.New("mux:packer: unpack err, content length too large")
		return
	}
	l, err = io.ReadFull(reader, Self.content[:Self.length])

	n += uint16(l)
	return
}

func (Self *basePackager) reset() {
	Self.length = 0
}

type muxPackager struct {
	flag      uint8
	id        int32
	window    uint64
	priority  bool
	queueNext *muxPackager
	basePackager
}

func (Self *muxPackager) Set(flag uint8, id int32, content interface{}) (err error) {
	Self.flag = flag
	Self.id = id
	switch flag {
	case muxPingFlag, muxPingReturn, muxNewMsg, muxNewMsgPart:
		if content == nil {
			return errors.New("mux:packer: data content is nil")
		}
		buf, ok := content.([]byte)
		if !ok {
			return fmt.Errorf("mux:packer: data content type %T, want []byte", content)
		}
		err = Self.basePackager.Set(buf)
	case muxMsgSendOk:
		// MUX_MSG_SEND_OK contains one data
		window, ok := content.(uint64)
		if !ok {
			return fmt.Errorf("mux:packer: window content type %T, want uint64", content)
		}
		Self.window = window
	default:
	}
	return
}

func (Self *muxPackager) Pack(writer io.Writer) (err error) {
	Self.head[0] = Self.flag
	binary.LittleEndian.PutUint32(Self.head[1:5], uint32(Self.id))
	switch Self.flag {
	case muxNewMsg, muxNewMsgPart, muxPingFlag, muxPingReturn:
		err = Self.basePackager.Pack(writer)
		if Self.content != nil {
			windowBuff.Put(Self.content)
			Self.content = nil
		}
	case muxMsgSendOk:
		binary.LittleEndian.PutUint64(Self.head[5:13], Self.window)
		_, err = writer.Write(Self.head[:13])
	default:
		_, err = writer.Write(Self.head[:5])
	}
	return
}

func (Self *muxPackager) UnPack(reader io.Reader) (n uint16, err error) {
	l, err := io.ReadFull(reader, Self.head[:5])
	if err != nil {
		return
	}
	n += uint16(l)
	Self.flag = Self.head[0]
	Self.id = int32(binary.LittleEndian.Uint32(Self.head[1:5]))
	switch Self.flag {
	case muxNewMsg, muxNewMsgPart, muxPingFlag, muxPingReturn:
		var m uint16
		m, err = Self.basePackager.UnPack(reader)
		n += m
	case muxMsgSendOk:
		l, err = io.ReadFull(reader, Self.head[5:13])
		if err == nil {
			Self.window = binary.LittleEndian.Uint64(Self.head[5:13])
			n += uint16(l) // uint64
		}
	default:
	}
	return
}

func (Self *muxPackager) reset() {
	Self.id = 0
	Self.flag = 0
	Self.length = 0
	Self.content = nil
	Self.window = 0
	Self.queueNext = nil
}
