package agent

import (
	"sync"
	"time"
)

// Buffer stores a session's raw terminal output with absolute offsets, so a
// command's output can be sliced out even after older output is dropped.
type Buffer struct {
	mu        sync.Mutex
	data      []byte
	base      int64 // absolute offset of data[0]
	max       int
	notify    chan struct{}
	lastWrite time.Time
}

// NewBuffer keeps roughly the last max bytes.
func NewBuffer(max int) *Buffer {
	return &Buffer{max: max, notify: make(chan struct{}), lastWrite: time.Now()}
}

// Write appends output and wakes anyone waiting for it.
func (b *Buffer) Write(p []byte) {
	b.mu.Lock()
	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		drop := len(b.data) - b.max*3/4
		b.data = append([]byte(nil), b.data[drop:]...)
		b.base += int64(drop)
	}
	b.lastWrite = time.Now()
	close(b.notify)
	b.notify = make(chan struct{})
	b.mu.Unlock()
}

// End is the absolute offset just past the newest byte.
func (b *Buffer) End() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.base + int64(len(b.data))
}

// Slice copies the bytes in [from, to), clamped to what is still held.
func (b *Buffer) Slice(from, to int64) []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	end := b.base + int64(len(b.data))
	if from < b.base {
		from = b.base
	}
	if to > end || to < 0 {
		to = end
	}
	if from >= to {
		return nil
	}
	return append([]byte(nil), b.data[from-b.base:to-b.base]...)
}

// Tail copies the last n bytes.
func (b *Buffer) Tail(n int) []byte {
	end := b.End()
	return b.Slice(end-int64(n), end)
}

// Changed returns a channel closed on the next write.
func (b *Buffer) Changed() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notify
}

// Idle reports how long since the last write.
func (b *Buffer) Idle() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Since(b.lastWrite)
}
