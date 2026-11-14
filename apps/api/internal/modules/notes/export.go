package notes

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
)

// Exports for other modules (docs): serde-like input decoding, typed note creation and the f64 printer.

// Obj is a request body decoded like serde would (see obj); getters return ErrFallback for input serde rejects.
type Obj struct{ o obj }

func ParseObj(input json.RawMessage) (Obj, error) { o, err := parseObj(input); return Obj{o}, err }

func (x Obj) ReqStr(k string) (string, error)   { return x.o.reqStr(k) }
func (x Obj) OptStr(k string) (*string, error)  { return x.o.optStr(k) }
func (x Obj) ReqUUID(k string) (string, error)  { return x.o.reqUUID(k) }
func (x Obj) OptUUID(k string) (*string, error) { return x.o.optUUID(k) }
func (x Obj) OptDate(k string) (*string, error) { return x.o.optDate(k) }
func (x Obj) OptInt32(k string) (*int64, error) { return x.o.optInt(k, 32) }

// ReqBool is a required bool: missing or null falls back.
func (x Obj) ReqBool(k string) (bool, error) {
	if _, ok := x.o[k]; !ok {
		return false, command.ErrFallback
	}
	return x.o.boolDef(k, false)
}

// InsertNote is Rust insert_note with no owner or verify_by override.
func InsertNote(c *command.Ctx, spaceID, title string, parentID, content *string, types []string) (Note, error) {
	n, err := insertNote(c, createNote{spaceID: spaceID, title: title, parentID: parentID, content: content, types: types})
	if err != nil {
		return Note{}, err
	}
	return n.(Note), nil
}

// F64 prints like serde_json prints an f64 (1.0, not 1).
type F64 = f64

// AddRelation is Rust's `add_relation`.
func AddRelation(c *command.Ctx, from, to, kind string) error {
	return addRelation(c, from, to, kind, nil)
}

// NormValue is command.Norm for jsonb read back through serde_json::Value (numbers re-printed as serde does).
func NormValue(raw []byte) (json.RawMessage, error) { return normValue(raw) }

func (x Obj) OptInt64(k string) (*int64, error)          { return x.o.optInt(k, 64) }
func (x Obj) OptValue(k string) (json.RawMessage, error) { return x.o.optValue(k) }

// Strings is `Vec<String>` with #[serde(default)]: missing is empty, null is an error.
func (x Obj) Strings(k string) ([]string, error) { return x.o.types(k) }

// Has reports a key that is present and not null.
func (x Obj) Has(k string) bool { _, ok := x.o.get(k); return ok }
