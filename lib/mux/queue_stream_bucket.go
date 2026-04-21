package mux

import "sync"

type streamBucketSet struct {
	buckets    map[int32]*streamBucket
	activeHead *streamBucket
	activeTail *streamBucket
}

func (s *streamBucketSet) init() {
	s.buckets = make(map[int32]*streamBucket)
}

func (s *streamBucketSet) hasPending() bool {
	return s.activeHead != nil
}

func (s *streamBucketSet) push(packager *muxPackager) {
	bucket := s.buckets[packager.id]
	if bucket == nil {
		bucket = getStreamBucket(packager.id)
		s.buckets[packager.id] = bucket
	}
	packager.queueNext = nil
	if bucket.tail == nil {
		bucket.head = packager
		bucket.tail = packager
	} else {
		bucket.tail.queueNext = packager
		bucket.tail = packager
	}
	if !bucket.active {
		s.appendActive(bucket)
	}
}

func (s *streamBucketSet) pop() *muxPackager {
	bucket := s.activeHead
	if bucket == nil || bucket.head == nil {
		return nil
	}
	packager := bucket.head
	bucket.head = packager.queueNext
	if bucket.head == nil {
		bucket.tail = nil
	}
	packager.queueNext = nil

	if bucket.head == nil {
		s.removeActive(bucket)
		delete(s.buckets, bucket.id)
		putStreamBucket(bucket)
	} else {
		s.moveActiveToTail(bucket)
	}
	return packager
}

func (s *streamBucketSet) appendActive(bucket *streamBucket) {
	bucket.active = true
	bucket.prevActive = s.activeTail
	bucket.nextActive = nil
	if s.activeTail != nil {
		s.activeTail.nextActive = bucket
	} else {
		s.activeHead = bucket
	}
	s.activeTail = bucket
}

func (s *streamBucketSet) moveActiveToTail(bucket *streamBucket) {
	if bucket == nil || bucket == s.activeTail {
		return
	}
	s.detachActive(bucket)
	s.appendActive(bucket)
}

func (s *streamBucketSet) removeActive(bucket *streamBucket) {
	if bucket == nil || !bucket.active {
		return
	}
	s.detachActive(bucket)
	bucket.active = false
}

func (s *streamBucketSet) detachActive(bucket *streamBucket) {
	if bucket.prevActive != nil {
		bucket.prevActive.nextActive = bucket.nextActive
	} else {
		s.activeHead = bucket.nextActive
	}
	if bucket.nextActive != nil {
		bucket.nextActive.prevActive = bucket.prevActive
	} else {
		s.activeTail = bucket.prevActive
	}
	bucket.prevActive = nil
	bucket.nextActive = nil
}

type streamBucket struct {
	id         int32
	head       *muxPackager
	tail       *muxPackager
	prevActive *streamBucket
	nextActive *streamBucket
	active     bool
}

var streamBucketPool = sync.Pool{
	New: func() any {
		return &streamBucket{}
	},
}

func getStreamBucket(id int32) *streamBucket {
	bucket := streamBucketPool.Get().(*streamBucket)
	bucket.id = id
	return bucket
}

func putStreamBucket(bucket *streamBucket) {
	*bucket = streamBucket{}
	streamBucketPool.Put(bucket)
}
