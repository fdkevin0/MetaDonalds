package backend

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type mediaReadFunc func([]byte) (int, error)

func (f mediaReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestIdleProgressReaderZeroBytesDoNotResetTimeout(t *testing.T) {
	var canceled atomic.Bool
	r := &idleProgressReader{
		body: mediaReadFunc(func([]byte) (int, error) {
			time.Sleep(10 * time.Millisecond)
			return 0, nil
		}),
		timeout: 50 * time.Millisecond,
		cancel:  func() { canceled.Store(true) },
	}
	start := time.Now()
	_, err := io.Copy(io.Discard, r)
	if !errors.Is(err, context.DeadlineExceeded) || !canceled.Load() || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("zero-byte reads bypassed idle timeout: elapsed=%s err=%v canceled=%v", time.Since(start), err, canceled.Load())
	}
}

func TestIdleProgressReaderStopsTimerOnClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var lateCancel atomic.Bool
	r := &idleProgressReader{
		body: mediaReadFunc(func([]byte) (int, error) {
			cancel()
			<-ctx.Done()
			return 0, ctx.Err()
		}),
		timeout: 60 * time.Millisecond,
		cancel:  func() { lateCancel.Store(true); cancel() },
	}
	_, err := r.Read(make([]byte, 8))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("client cancellation: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if lateCancel.Load() {
		t.Fatal("idle timer canceled a request after its body read completed")
	}
}
