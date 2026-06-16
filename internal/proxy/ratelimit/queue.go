package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrQueueFull    = errors.New("rate limit queue full")
	ErrQueueTimeout = errors.New("rate limit queue wait timed out")
)

type waiter struct {
	ready chan struct{}
}

type queue struct {
	mu            sync.Mutex
	active        int
	maxConcurrent int
	maxQueueSize  int
	queueTimeout  time.Duration
	waiters       []*waiter
}

func newQueue(maxConcurrent, maxQueueSize int, queueTimeout time.Duration) *queue {
	return &queue{
		maxConcurrent: maxConcurrent,
		maxQueueSize:  maxQueueSize,
		queueTimeout:  queueTimeout,
	}
}

func (q *queue) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	q.mu.Lock()
	if len(q.waiters) == 0 && q.active < q.maxConcurrent {
		q.active++
		q.mu.Unlock()
		return nil
	}

	if q.maxQueueSize <= 0 || len(q.waiters) >= q.maxQueueSize {
		q.mu.Unlock()
		return ErrQueueFull
	}

	w := &waiter{ready: make(chan struct{}, 1)}
	q.waiters = append(q.waiters, w)
	timeout := q.queueTimeout
	q.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-w.ready:
		return nil
	case <-timer.C:
		if q.tryDepart(w) {
			return ErrQueueTimeout
		}
		return nil
	case <-ctx.Done():
		if q.tryDepart(w) {
			return ctx.Err()
		}
		return nil
	}
}

// tryDepart atomically removes w from the waiters list.
// Returns true if w was found and removed (caller should return its error).
// Returns false if w was already served by release (caller got the slot).
func (q *queue) tryDepart(w *waiter) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, item := range q.waiters {
		if item == w {
			q.waiters = append(q.waiters[:i], q.waiters[i+1:]...)
			return true
		}
	}
	return false
}

func (q *queue) release() {
	q.mu.Lock()
	q.active--
	for q.active < q.maxConcurrent && len(q.waiters) > 0 {
		w := q.waiters[0]
		q.waiters = q.waiters[1:]
		select {
		case w.ready <- struct{}{}:
			q.active++
			q.mu.Unlock()
			return
		default:
		}
	}
	q.mu.Unlock()
}

func (q *queue) waitingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiters)
}

type Manager struct {
	mu     sync.Mutex
	queues map[string]*queue
}

func NewManager() *Manager {
	return &Manager{queues: make(map[string]*queue)}
}

type AcquireResult struct {
	Release  func()
	Queued   bool
	WaitTime time.Duration
}

func (m *Manager) Acquire(ctx context.Context, providerID string, maxConcurrent, maxQueueSize, queueTimeoutMS int) (*AcquireResult, error) {
	if maxConcurrent <= 0 {
		return &AcquireResult{Release: func(){}}, nil
	}

	queueTimeout := time.Duration(queueTimeoutMS) * time.Millisecond
	if queueTimeout <= 0 {
		queueTimeout = 60 * time.Second
	}

	q := m.getOrCreate(providerID, maxConcurrent, maxQueueSize, queueTimeout)

	start := time.Now()
	if err := q.acquire(ctx); err != nil {
		return nil, err
	}
	waitDuration := time.Since(start)

	return &AcquireResult{
		Release:  func() { q.release() },
		Queued:   waitDuration > time.Millisecond,
		WaitTime: waitDuration,
	}, nil
}

func (m *Manager) getOrCreate(providerID string, maxConcurrent, maxQueueSize int, queueTimeout time.Duration) *queue {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.queues[providerID]
	if !ok {
		q = newQueue(maxConcurrent, maxQueueSize, queueTimeout)
		m.queues[providerID] = q
		return q
	}

	q.mu.Lock()
	q.maxConcurrent = maxConcurrent
	q.maxQueueSize = maxQueueSize
	q.queueTimeout = queueTimeout
	q.mu.Unlock()
	return q
}
