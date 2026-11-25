package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/cnote/api/internal/modules/notes"
)

// TrashPurgeSweep removes trashed pages whose retention period is over (docs C1 5.6). Per workspace it purges a small batch,
// each deletion in its own short transaction; a failure in one workspace does not stop the others.
func TrashPurgeSweep(ctx context.Context, w *Worker) error {
	ids, err := allWorkspaceIDs(ctx, w.Pool)
	if err != nil {
		return err
	}
	var errs []error
	for _, ws := range ids {
		if ctx.Err() != nil {
			break
		}
		if _, err := notes.PurgeDue(ctx, w.Pool, ws); err != nil {
			errs = append(errs, fmt.Errorf("workspace %s: %w", ws, err))
		}
	}
	return errors.Join(errs...)
}
