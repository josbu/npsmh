package mux

import (
	"testing"
	"unsafe"
)

func TestBufDequeueFIFO(t *testing.T) {
	var d bufDequeue
	d.vals = make([]unsafe.Pointer, 8)

	values := []*int{new(int), new(int), new(int)}
	*values[0] = 1
	*values[1] = 2
	*values[2] = 3

	for i, v := range values {
		if ok := d.pushHead(unsafe.Pointer(v)); !ok {
			t.Fatalf("pushHead(%d) failed", i)
		}
	}

	for i, want := range values {
		raw, ok := d.popTail()
		if !ok {
			t.Fatalf("popTail(%d) reported empty queue", i)
		}
		got := (*int)(raw)
		if got != want || *got != *want {
			t.Fatalf("popTail(%d) = %v/%d, want %v/%d", i, got, *got, want, *want)
		}
	}

	if _, ok := d.popTail(); ok {
		t.Fatal("popTail() should report empty queue after draining all values")
	}
}

func TestBufChainFIFOAcrossGrowth(t *testing.T) {
	var c bufChain
	c.new(2)

	const total = 10
	values := make([]*int, 0, total)
	for i := 0; i < total; i++ {
		v := new(int)
		*v = i
		values = append(values, v)
		c.pushHead(unsafe.Pointer(v))
	}

	for i, want := range values {
		raw, ok := c.popTail()
		if !ok {
			t.Fatalf("popTail(%d) reported empty queue", i)
		}
		got := (*int)(raw)
		if got != want || *got != *want {
			t.Fatalf("popTail(%d) = %v/%d, want %v/%d", i, got, *got, want, *want)
		}
	}

	if _, ok := c.popTail(); ok {
		t.Fatal("popTail() should report empty queue after draining grown chain")
	}
}
