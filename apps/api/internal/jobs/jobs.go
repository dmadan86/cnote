// Package jobs holds the types shared by the Go worker and the per-module handlers (port of the
// Event/Job rows of crates/cnote-core/src/events.rs and crates/cnote-server/src/worker.rs).
package jobs

import (
	"context"
	"encoding/json"
	"time"
)

// Event is a core.events row. Rust serializes it as a JSON object with exactly these keys
// (created_at as chrono RFC 3339), which outbound hooks embed in their body; use EventJSON for that.
type Event struct {
	ID          int64
	WorkspaceID string
	NoteID      *string
	Kind        string
	ActorType   string
	ActorID     *string
	OnBehalfOf  *string
	Payload     json.RawMessage
	CreatedAt   time.Time
}

// Job is a claimed core.jobs row.
type Job struct {
	ID          int64
	WorkspaceID *string
	Kind        string
	Payload     json.RawMessage
	Attempts    int32
	MaxAttempts int32
}

type jobIDKey struct{}

// WithJobID tags ctx with the core.jobs id being run, so outbound effects can send a per-job idempotency key.
func WithJobID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, jobIDKey{}, id)
}

// JobID returns the id set by WithJobID.
func JobID(ctx context.Context) (int64, bool) { id, ok := ctx.Value(jobIDKey{}).(int64); return id, ok }
