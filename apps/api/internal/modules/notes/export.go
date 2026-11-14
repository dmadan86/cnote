package notes

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
)

// InsertNote is Rust `notes::insert_note`, shared by modules that create typed notes (items, deals...).
// It returns the new note's id.
func InsertNote(c *command.Ctx, spaceID, title string, parentID, contentMD *string, types []string) (string, error) {
	out, err := insertNote(c, createNote{spaceID: spaceID, title: title, parentID: parentID, content: contentMD, types: types})
	if err != nil {
		return "", err
	}
	return out.(Note).ID, nil
}

// F64 serializes like serde_json prints an f64 (3 -> 3.0).
type F64 = f64

// NormValue is command.Norm for jsonb read back through serde_json::Value (numbers re-printed as serde does).
func NormValue(raw []byte) (json.RawMessage, error) { return normValue(raw) }
