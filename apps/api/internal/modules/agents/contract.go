// Package agents is the AI employee runtime (docs/design/agent-runtime.md). It talks to other modules only through
// registry commands and events, and reads only its own tables plus core.confirmations.
// Wave 0: signatures only; later waves fill the bodies.
package agents

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/modules/ai"
)

var errNotImplemented = errors.New("agents: not implemented")

// NewRun is one request to run an agent. Trigger is ask | chat | event | schedule | manual.
type NewRun struct {
	AgentID, OnBehalfOf, Trigger, Input string
	DedupeKey, ConversationID, NoteID   *string
	Context                             any
}

// Model is the seam tests fake: one model call.
type Model func(context.Context, ai.StepRequest) (ai.StepResult, error)

// CreateRun inserts the run row and its job in c.Tx. created is false when the dedupe key already exists.
func CreateRun(c *command.Ctx, n NewRun) (id string, created bool, err error) {
	return createRun(c, n)
}

// RunJob is the long-lane handler for agent.ask and agent.run jobs.
func RunJob(pool *pgxpool.Pool, reg *command.Registry) func(context.Context, jobs.Job) error {
	return runJob(pool, reg, nil)
}
