package publiclinks

import (
	"strconv"
	"sync"
	"time"
)

// limiter is a keyed token bucket. shortcut: in-process, so with several API instances each has its own budget;
// move to a shared store when the API is scaled out.
type limiter struct {
	mu     sync.Mutex
	b      map[string]*bkt
	burst  float64
	perSec float64
}

type bkt struct {
	tokens float64
	last   time.Time
}

func newLimiter(burst, perSec float64) *limiter {
	return &limiter{b: map[string]*bkt{}, burst: burst, perSec: perSec}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.b) > 10000 {
		for k, x := range l.b {
			if now.Sub(x.last) > 10*time.Minute {
				delete(l.b, k)
			}
		}
	}
	x, ok := l.b[key]
	if !ok {
		x = &bkt{tokens: l.burst, last: now}
		l.b[key] = x
	}
	x.tokens = min(x.tokens+now.Sub(x.last).Seconds()*l.perSec, l.burst)
	x.last = now
	if x.tokens < 1 {
		return false
	}
	x.tokens--
	return true
}

// peek reports whether a token is available without taking it.
func (l *limiter) peek(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	x, ok := l.b[key]
	if !ok {
		return true
	}
	return min(x.tokens+time.Since(x.last).Seconds()*l.perSec, l.burst) >= 1
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(getenv(k), 64); err == nil && v > 0 {
		return v
	}
	return def
}
