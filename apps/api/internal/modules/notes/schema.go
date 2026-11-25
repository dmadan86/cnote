package notes

import "github.com/cnote/api/internal/command"

// DocSchema must equal DOC_SCHEMA in apps/collab/src/doc-schema.ts (schema_test.go checks it). docs-parity 7.3.
const DocSchema = 2

// fenceBodySave refuses a body save from an editor that may flatten blocks it does not know. Only people's
// editors (web tabs, the mobile app) are fenced. Not fenced, and why:
//   - API keys and AI employees (machine principals) send whole Markdown and run no editor converter, so they
//     cannot flatten a block; the gateway re-parses what they write.
//   - nested calls (accepted AI edits, project item descriptions) are server code at the current version.
//   - import, template render, note.restore, GDPR erase write server side; restore also bumps collab_epoch,
//     which fences every open room and old mobile saves.
func fenceBodySave(c *command.Ctx, hasBody bool, schema, expectedEpoch *int64) error {
	if !hasBody || c.Principal.Kind != command.KindUser || c.Nested() {
		return nil
	}
	if schema == nil || *schema < DocSchema {
		return command.NewError(command.UpgradeRequired, "C Note was updated. Reload or update the app to keep editing; you can still read.")
	}
	if expectedEpoch == nil {
		return command.ErrInvalid("expected_epoch is required when saving content")
	}
	return nil
}
