package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Bump counts one hit on key in the current hour (or UTC day) and returns the count so far. Buckets live in
// core.rate_buckets, so every API replica sees the same numbers. The key must not hold a raw address: callers pass a
// keyed hash. PruneBuckets removes old rows.
func Bump(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, key string, day bool) (int, error) {
	var n int
	err := q.QueryRow(ctx, `insert into core.rate_buckets (key, bucket, n) values ($1, $2, 1)
		on conflict (key, bucket) do update set n = core.rate_buckets.n + 1 returning n`, key, BucketStart(time.Now(), day)).Scan(&n)
	return n, err
}

// BucketStart is the start of the current window.
func BucketStart(now time.Time, day bool) time.Time {
	if day {
		return now.UTC().Truncate(24 * time.Hour)
	}
	return now.UTC().Truncate(time.Hour)
}

// PruneBuckets deletes counters older than two days.
func PruneBuckets(ctx context.Context, q interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}) error {
	_, err := q.Exec(ctx, "delete from core.rate_buckets where bucket < now() - interval '2 days'")
	return err
}
