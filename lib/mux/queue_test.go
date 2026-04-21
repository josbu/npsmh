package mux

import "testing"

func newTestPack(flag uint8, id int32, priority bool) *muxPackager {
	return &muxPackager{
		flag:     flag,
		id:       id,
		priority: priority,
	}
}

func popPackOrFatal(t *testing.T, q *priorityQueue) *muxPackager {
	t.Helper()
	pack := q.TryPop()
	if pack == nil {
		t.Fatal("TryPop() returned nil")
	}
	return pack
}

func TestPriorityQueueRoundRobinByStream(t *testing.T) {
	var q priorityQueue
	q.New()

	q.Push(newTestPack(muxNewMsg, 1, false))
	q.Push(newTestPack(muxNewMsg, 1, false))
	q.Push(newTestPack(muxNewMsg, 1, false))
	q.Push(newTestPack(muxNewMsg, 3, false))
	q.Push(newTestPack(muxNewMsg, 5, false))

	var got []int32
	for i := 0; i < 5; i++ {
		got = append(got, popPackOrFatal(t, &q).id)
	}

	want := []int32{1, 3, 5, 1, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pop order = %v, want %v", got, want)
		}
	}
}

func TestPriorityQueueControlFramesBypassStreamBacklog(t *testing.T) {
	var q priorityQueue
	q.New()

	q.Push(newTestPack(muxNewMsg, 1, false))
	q.Push(newTestPack(muxNewMsgPart, 1, false))
	q.Push(newTestPack(muxMsgSendOk, 1, false))

	first := popPackOrFatal(t, &q)
	if first.flag != muxMsgSendOk {
		t.Fatalf("first flag = %d, want %d", first.flag, muxMsgSendOk)
	}

	second := popPackOrFatal(t, &q)
	third := popPackOrFatal(t, &q)
	if second.flag != muxNewMsg || third.flag != muxNewMsgPart {
		t.Fatalf("stream flags after control = [%d %d], want [%d %d]", second.flag, third.flag, muxNewMsg, muxNewMsgPart)
	}
}

func TestPriorityQueueOrderedClosePreservesStreamOrder(t *testing.T) {
	var q priorityQueue
	q.New()

	q.Push(newTestPack(muxNewMsg, 1, false))
	q.Push(newTestPack(muxConnClose, 1, false))
	q.Push(newTestPack(muxNewMsg, 3, false))

	first := popPackOrFatal(t, &q)
	second := popPackOrFatal(t, &q)
	third := popPackOrFatal(t, &q)

	if first.id != 1 || first.flag != muxNewMsg {
		t.Fatalf("first pop = (id=%d flag=%d), want stream 1 data", first.id, first.flag)
	}
	if second.id != 3 || second.flag != muxNewMsg {
		t.Fatalf("second pop = (id=%d flag=%d), want stream 3 data", second.id, second.flag)
	}
	if third.id != 1 || third.flag != muxConnClose {
		t.Fatalf("third pop = (id=%d flag=%d), want stream 1 close after prior data", third.id, third.flag)
	}
}

func TestPriorityQueuePriorityBurstStillServesNormalStream(t *testing.T) {
	var q priorityQueue
	q.New()

	for i := 0; i < int(maxPriorityBurst)+2; i++ {
		q.Push(newTestPack(muxNewMsg, 1, true))
	}
	q.Push(newTestPack(muxNewMsg, 3, false))

	var seenNormal bool
	for i := 0; i < int(maxPriorityBurst)+1; i++ {
		pack := popPackOrFatal(t, &q)
		if pack.id == 3 {
			seenNormal = true
			break
		}
	}

	if !seenNormal {
		t.Fatalf("normal stream was not scheduled within %d pops", maxPriorityBurst+1)
	}
}
