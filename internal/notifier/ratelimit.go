package notifier

import (
	"context"
	"sync"
	"time"
)

// tokenBucket implements a simple token bucket rate limiter.
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	rate     float64 // tokens per second
	burst    float64
	lastTime time.Time
}

func newTokenBucket(rate float64, burst int) *tokenBucket {
	return &tokenBucket{
		tokens:   float64(burst),
		rate:     rate,
		burst:    float64(burst),
		lastTime: time.Now(),
	}
}

// wait blocks until a token is available or the context is cancelled.
func (tb *tokenBucket) wait(ctx context.Context) {
	for {
		if tb.tryAcquire() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (tb *tokenBucket) tryAcquire() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(tb.lastTime).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.burst {
		tb.tokens = tb.burst
	}
	tb.lastTime = now
	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}
	return false
}
