package command

import (
	"os"
	"strconv"
	"sync"
	"time"
)

// Per-seat fair-use token bucket (ratelimit.rs). shortcut: separate in-process bucket from Rust's, so a
// caller can briefly use both budgets while commands are split; ends when the last module is ported.
var (
	bmu     sync.Mutex
	buckets = map[string]*bucket{}
)

type bucket struct {
	tokens float64
	last   time.Time
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}

func checkRate(key string) error {
	return CheckRateWith(key, envFloat("FAIR_USE_BURST", 240), envFloat("FAIR_USE_PER_SEC", 20))
}

// CheckRateWith is Rust's ratelimit::check_with: the same bucket store with explicit burst and refill per second, for
// unauthenticated endpoints (OAuth). Returns a RateLimited error when the bucket is empty.
func CheckRateWith(key string, burst, perSec float64) error {
	if len(key) > 128 {
		key = key[:128]
	}
	bmu.Lock()
	defer bmu.Unlock()
	now := time.Now()
	if len(buckets) > 1024 {
		for k, b := range buckets {
			if now.Sub(b.last) >= 10*time.Minute {
				delete(buckets, k)
			}
		}
	}
	b, ok := buckets[key]
	if !ok {
		b = &bucket{tokens: burst, last: now}
		buckets[key] = b
	}
	b.tokens = min(b.tokens+now.Sub(b.last).Seconds()*perSec, burst)
	b.last = now
	if b.tokens < 1 {
		return NewError(RateLimited, "fair-use rate limit reached, retry shortly")
	}
	b.tokens--
	return nil
}
