package ratelimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueue_Concurrency(t *testing.T) {
	q := newQueue(3, 20, 5*time.Second)
	var active int32
	var maxActive int32
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := q.acquire(context.Background()); err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}
			defer q.release()

			cur := atomic.AddInt32(&active, 1)
			for {
				max := atomic.LoadInt32(&maxActive)
				if cur <= max || atomic.CompareAndSwapInt32(&maxActive, max, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	wg.Wait()

	if maxActive > 3 {
		t.Errorf("max concurrent = %d, want <= 3", maxActive)
	}
}

func TestQueue_FIFO(t *testing.T) {
	q := newQueue(1, 10, 10*time.Second)

	// Occupy the single slot
	if err := q.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Start goroutines sequentially to ensure FIFO queue ordering
	for i := 0; i < 5; i++ {
		wg.Add(1)
		id := i
		go func() {
			defer wg.Done()
			if err := q.acquire(context.Background()); err != nil {
				t.Errorf("acquire %d failed: %v", id, err)
				return
			}
			defer q.release()
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
		}()
		// Give each goroutine time to queue before starting the next
		time.Sleep(10 * time.Millisecond)
	}
	// All 5 goroutines are now waiting in the queue
	q.release() // free the slot, start the chain
	wg.Wait()

	if len(order) != 5 {
		t.Fatalf("got %d completions, want 5", len(order))
	}
	for i := 0; i < 5; i++ {
		if order[i] != i {
			t.Errorf("order[%d] = %d, want %d (FIFO violated)", i, order[i], i)
		}
	}
}

func TestQueue_StrictFIFO_NoBypass(t *testing.T) {
	q := newQueue(1, 10, 5*time.Second)

	if err := q.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Queue 3 waiters
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.acquire(context.Background())
			defer q.release()
		}()
		time.Sleep(10 * time.Millisecond)
	}

	// Now release the slot; immediately try to acquire (simulating a new request)
	// The new request should NOT bypass the 3 waiting goroutines
	q.release()
	time.Sleep(50 * time.Millisecond)

	// At this point, all 3 waiters should have been served and released.
	// A new acquire should succeed immediately (no waiters left).
	start := time.Now()
	if err := q.acquire(context.Background()); err != nil {
		t.Fatalf("post-chain acquire failed: %v", err)
	}
	elapsed := time.Since(start)
	q.release()
	wg.Wait()

	if elapsed > 100*time.Millisecond {
		t.Errorf("new acquire took %v, expected fast path (waiters should be gone)", elapsed)
	}
}

func TestQueue_QueueFull(t *testing.T) {
	q := newQueue(1, 0, time.Second)

	if err := q.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.release()

	err := q.acquire(context.Background())
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("got %v, want ErrQueueFull", err)
	}
}

func TestQueue_QueueFull_WithCapacity(t *testing.T) {
	q := newQueue(1, 2, 5*time.Second)

	if err := q.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Second acquire should queue, not reject
	done := make(chan error, 1)
	go func() {
		done <- q.acquire(context.Background())
	}()

	// Verify it's queued, not rejected immediately
	select {
	case err := <-done:
		t.Errorf("second acquire should have queued, got: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	// Release and let the queued request proceed
	q.release()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("second acquire failed: %v", err)
		}
		q.release()
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire timed out after release")
	}
}

func TestQueue_Timeout(t *testing.T) {
	q := newQueue(1, 5, 50*time.Millisecond)

	// Occupy the slot
	if err := q.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.release()

	start := time.Now()
	err := q.acquire(context.Background())
	elapsed := time.Since(start)

	if !errors.Is(err, ErrQueueTimeout) {
		t.Errorf("got %v, want ErrQueueTimeout", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("elapsed %v too short, expected ~50ms", elapsed)
	}
}

func TestQueue_ContextCancel(t *testing.T) {
	q := newQueue(1, 5, 10*time.Second)

	// Occupy the slot
	if err := q.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer q.release()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	err := q.acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}

	// Verify no slot leak: a new acquire should succeed immediately after release
	done := make(chan error, 1)
	go func() {
		done <- q.acquire(context.Background())
	}()
	q.release()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("acquire after cancel failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("acquire after cancel timed out (slot leaked?)")
	}
}

func TestQueue_ReleaseAfterTimeout(t *testing.T) {
	q := newQueue(1, 5, 50*time.Millisecond)

	if err := q.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	gotSlot := make(chan bool, 1)
	go func() {
		err := q.acquire(context.Background())
		if err != nil {
			gotSlot <- false
		} else {
			gotSlot <- true
			q.release()
		}
	}()

	time.Sleep(10 * time.Millisecond) // ensure waiter is queued
	q.release()                        // free the slot
	time.Sleep(100 * time.Millisecond) // wait for timeout/release

	// No leaked slots: a new acquire should succeed
	if err := q.acquire(context.Background()); err != nil {
		t.Errorf("post-timeout acquire failed: %v", err)
	} else {
		q.release()
	}
}

func TestManager_DisabledProvider(t *testing.T) {
	m := NewManager()
	result, err := m.Acquire(context.Background(), "p1", 0, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Queued {
		t.Error("should not be queued when maxConcurrent=0")
	}
	result.Release()
}

func TestManager_AcquireAndRelease(t *testing.T) {
	m := NewManager()

	r1, err := m.Acquire(context.Background(), "p1", 2, 5, 5000)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := m.Acquire(context.Background(), "p1", 2, 5, 5000)
	if err != nil {
		t.Fatal(err)
	}

	// Third should queue
	done := make(chan error, 1)
	go func() {
		r3, err := m.Acquire(context.Background(), "p1", 2, 5, 5000)
		if err != nil {
			done <- err
			return
		}
		r3.Release()
		done <- nil
	}()

	select {
	case err := <-done:
		t.Fatalf("third acquire should have queued, got: %v", err)
	case <-time.After(20 * time.Millisecond):
		// expected: still queued
	}

	r2.Release()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("third acquire failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("third acquire timed out")
	}

	r1.Release()
}

func TestManager_ConfigChange(t *testing.T) {
	m := NewManager()

	r1, err := m.Acquire(context.Background(), "p1", 2, 5, 5000)
	if err != nil {
		t.Fatal(err)
	}
	r1.Release()

	// Change config: maxConcurrent from 2 to 5
	r2, err := m.Acquire(context.Background(), "p1", 5, 5, 5000)
	if err != nil {
		t.Fatal(err)
	}
	r2.Release()

	// Verify the new queue has 5 capacity
	var wg sync.WaitGroup
	var active int32
	var maxActive int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := m.Acquire(context.Background(), "p1", 5, 5, 5000)
			if err != nil {
				return
			}
			defer r.Release()
			cur := atomic.AddInt32(&active, 1)
			for {
				mx := atomic.LoadInt32(&maxActive)
				if cur <= mx || atomic.CompareAndSwapInt32(&maxActive, mx, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	wg.Wait()

	if maxActive > 5 {
		t.Errorf("max concurrent = %d, want <= 5", maxActive)
	}
	if maxActive < 5 {
		t.Errorf("max concurrent = %d, expected to reach 5 with 10 goroutines", maxActive)
	}
}

func TestManager_ConfigChange_InFlightRespected(t *testing.T) {
	m := NewManager()

	r1, _ := m.Acquire(context.Background(), "p1", 3, 5, 5000)
	r2, _ := m.Acquire(context.Background(), "p1", 3, 5, 5000)
	r3, _ := m.Acquire(context.Background(), "p1", 3, 5, 5000)

	// Reduce cap to 1 while 3 are in-flight. New acquire should queue.
	done := make(chan error, 1)
	go func() {
		r, err := m.Acquire(context.Background(), "p1", 1, 5, 5000)
		if err != nil {
			done <- err
			return
		}
		r.Release()
		done <- nil
	}()

	select {
	case err := <-done:
		t.Fatalf("new acquire should queue (active=3, max=1), got: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	// Release one: active=2, still > max=1
	r1.Release()
	select {
	case err := <-done:
		t.Fatalf("new acquire should still queue (active=2, max=1), got: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	// Release one more: active=1, still not < max=1
	r2.Release()
	select {
	case err := <-done:
		t.Fatalf("new acquire should still queue (active=1, max=1), got: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	// Release last: active=0 < max=1, waiter gets served
	r3.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("new acquire failed after all released: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new acquire timed out after all in-flight released")
	}
}

func TestQueue_StrictFIFO_BarrierRace(t *testing.T) {
	q := newQueue(1, 10, 5*time.Second)

	if err := q.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	w1Served := make(chan struct{})
	go func() {
		if err := q.acquire(context.Background()); err != nil {
			close(w1Served)
			return
		}
		close(w1Served)
		time.Sleep(50 * time.Millisecond)
		q.release()
	}()
	time.Sleep(20 * time.Millisecond)

	if q.waitingCount() != 1 {
		t.Fatalf("expected 1 waiter, got %d", q.waitingCount())
	}

	barrier := make(chan struct{})
	newAcquireAcquired := make(chan struct{})
	newAcquireResult := make(chan error, 1)

	go func() {
		<-barrier
		q.release()
	}()

	go func() {
		<-barrier
		err := q.acquire(context.Background())
		if err == nil {
			close(newAcquireAcquired)
			q.release()
		}
		newAcquireResult <- err
	}()

	close(barrier)

	select {
	case <-w1Served:
	case <-time.After(2 * time.Second):
		t.Fatal("w1 was not served within 2s (deadlock or bypass)")
	}

	select {
	case <-newAcquireAcquired:
		t.Fatal("new acquire got slot while w1 still holds it (FIFO bypass)")
	case <-time.After(30 * time.Millisecond):
	}

	<-newAcquireResult
}

func TestQueue_Acquire_CancelledContext(t *testing.T) {
	q := newQueue(1, 5, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := q.acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestParseRetryAfter_Seconds(t *testing.T) {
	resp := &http.Response{}
	resp.Header = http.Header{}
	resp.Header.Set("Retry-After", "3")
	d := parseRetryAfter(resp, 10*time.Second)
	if d != 3*time.Second {
		t.Errorf("got %v, want 3s", d)
	}
}

func TestParseRetryAfter_Capped(t *testing.T) {
	resp := &http.Response{}
	resp.Header = http.Header{}
	resp.Header.Set("Retry-After", "30")
	d := parseRetryAfter(resp, 5*time.Second)
	if d != 5*time.Second {
		t.Errorf("got %v, want 5s (capped)", d)
	}
}

func TestParseRetryAfter_Absent(t *testing.T) {
	resp := &http.Response{}
	resp.Header = http.Header{}
	d := parseRetryAfter(resp, 10*time.Second)
	if d != 0 {
		t.Errorf("got %v, want 0", d)
	}
}

func TestParse429Body_Code1302(t *testing.T) {
	body := []byte(`{"error":{"type":"rate_limit","code":1302},"message":"Too many concurrent requests"}`)
	code, msg := parse429Body(body)
	if code != code1302 {
		t.Errorf("code = %d, want 1302", code)
	}
	if msg != "Too many concurrent requests" {
		t.Errorf("msg = %s", msg)
	}
}

func TestParse429Body_Code1305(t *testing.T) {
	body := []byte(`{"error":{"type":"overloaded","code":1305},"message":"Service overloaded"}`)
	code, msg := parse429Body(body)
	if code != code1305 {
		t.Errorf("code = %d, want 1305", code)
	}
	if msg == "" {
		t.Error("msg should not be empty")
	}
}

func TestParse429Body_NonJSON(t *testing.T) {
	code, _ := parse429Body([]byte("Service Unavailable"))
	if code != codeUnknown {
		t.Errorf("code = %d, want codeUnknown", code)
	}
}

func TestComputeBackoff(t *testing.T) {
	cfg := retry429Config{
		initialDelay: 100 * time.Millisecond,
		maxDelay:     5 * time.Second,
	}

	for attempt := 0; attempt < 5; attempt++ {
		d := computeBackoff(cfg, attempt)
		expected := time.Duration(float64(cfg.initialDelay) * float64(int(1)<<attempt))
		// d should be at least expected (plus jitter), at most maxDelay+250ms
		if d < expected {
			t.Errorf("attempt %d: delay %v < expected %v", attempt, d, expected)
		}
		if d > cfg.maxDelay+250*time.Millisecond {
			t.Errorf("attempt %d: delay %v > max %v", attempt, d, cfg.maxDelay+250*time.Millisecond)
		}
	}
}

func TestRetry429_SuccessAfterRetry(t *testing.T) {
	calls := 0
	doRequest := func() (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{
				StatusCode: 429,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"code":1302}}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	}

	cfg := retry429Config{
		maxAttempts:  3,
		initialDelay: 1 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	resp, err := retry429(context.Background(), doRequest, cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	resp.Body.Close()
}

func TestRetry429_MaxAttemptsExhausted(t *testing.T) {
	calls := 0
	doRequest := func() (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: 429,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":1305}}`)),
		}, nil
	}

	cfg := retry429Config{
		maxAttempts:  2,
		initialDelay: 1 * time.Millisecond,
		maxDelay:     5 * time.Millisecond,
	}

	resp, err := retry429(context.Background(), doRequest, cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 429 {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("calls = %d, want 3", calls)
	}
	resp.Body.Close()
}

func TestRetry429_Non429PassThrough(t *testing.T) {
	doRequest := func() (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("err")),
		}, nil
	}

	cfg := retry429Config{
		maxAttempts:  3,
		initialDelay: 1 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	resp, err := retry429(context.Background(), doRequest, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRetry429_CancelDuringBackoff(t *testing.T) {
	calls := 0
	doRequest := func() (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: 429,
			Header:     http.Header{"Retry-After": []string{"10"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":1305}}`)),
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	cfg := retry429Config{
		maxAttempts:  3,
		initialDelay: 1 * time.Millisecond,
		maxDelay:     10 * time.Second,
	}

	_, err := retry429(ctx, doRequest, cfg, nil)
	if err == nil {
		t.Error("expected cancellation error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (should not retry after cancel)", calls)
	}
}
