package mux

import (
	"bytes"
	"testing"
)

func BenchmarkMuxPackagerPackData(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024)
	var writer bytes.Buffer
	writer.Grow(len(payload) + 16)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer.Reset()
		pack := muxPack.Get()
		if err := pack.Set(muxNewMsg, 1, payload); err != nil {
			b.Fatal(err)
		}
		if err := pack.Pack(&writer); err != nil {
			b.Fatal(err)
		}
		muxPack.Put(pack)
	}
}

func BenchmarkMuxPackagerUnpackData(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024)
	var encoded bytes.Buffer
	encoded.Grow(len(payload) + 16)

	pack := muxPack.Get()
	if err := pack.Set(muxNewMsg, 1, payload); err != nil {
		b.Fatal(err)
	}
	if err := pack.Pack(&encoded); err != nil {
		b.Fatal(err)
	}
	muxPack.Put(pack)

	packet := append([]byte(nil), encoded.Bytes()...)
	var reader bytes.Reader

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader.Reset(packet)
		pack := muxPack.Get()
		if _, err := pack.UnPack(&reader); err != nil {
			b.Fatal(err)
		}
		if pack.content != nil {
			windowBuff.Put(pack.content)
			pack.content = nil
		}
		muxPack.Put(pack)
	}
}
