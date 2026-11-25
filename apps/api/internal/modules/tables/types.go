package tables

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/cnote/api/internal/command"
)

// Shared contracts for work packages 1 (fields, structure) and 2 (records, query). Change a signature here only
// with both owners; everything else in the package can move freely.

type FieldType string

const (
	Text         FieldType = "text"
	LongText     FieldType = "long_text"
	Number       FieldType = "number"
	Percent      FieldType = "percent"
	Currency     FieldType = "currency"
	Date         FieldType = "date"
	Checkbox     FieldType = "checkbox"
	SingleSelect FieldType = "single_select"
	MultiSelect  FieldType = "multi_select"
	Phone        FieldType = "phone"
	Email        FieldType = "email"
	URL          FieldType = "url"
	User         FieldType = "user"
	Attachment   FieldType = "attachment" // 1b: stored but no command writes it in 1a
	Link         FieldType = "link"
	Autonumber   FieldType = "autonumber"
	CreatedTime  FieldType = "created_time"
	ModifiedTime FieldType = "modified_time"
	CreatedBy    FieldType = "created_by"
	ModifiedBy   FieldType = "modified_by"
)

// AllFieldTypes is the order shown in the field dialog. It must match the CHECK on tables.fields.type.
var AllFieldTypes = []FieldType{Text, LongText, Number, Percent, Currency, Date, Checkbox, SingleSelect, MultiSelect,
	Phone, Email, URL, User, Attachment, Link, Autonumber, CreatedTime, ModifiedTime, CreatedBy, ModifiedBy}

// Computed fields store nothing in records.data: autonumber reads records.seq, the others read record columns, and a
// link reads tables.links. They are never written through values.
func (t FieldType) Computed() bool {
	switch t {
	case Autonumber, CreatedTime, ModifiedTime, CreatedBy, ModifiedBy, Link:
		return true
	}
	return false
}

// Field config structs, one per type that has config (stored as tables.fields.config). Types not listed have none
// and keep "{}". ConfigFor returns a pointer to decode into.
type Option struct {
	ID    string `json:"id"` // "o1", "o2"...; renaming an option touches no record
	Name  string `json:"name"`
	Color string `json:"color"`
}
type NumberConfig struct {
	Decimals int `json:"decimals"` // number, percent: 0-4; currency: 0 or 2
}
type SelectConfig struct {
	Options []Option `json:"options"` // at most 200 (single) or 200 with at most 50 chosen (multi)
}
type UserConfig struct {
	Multiple bool `json:"multiple"`
}
type LinkConfig struct {
	TableID   string `json:"table_id"`             // target table, same base only
	Multiple  bool   `json:"multiple"`             // false: at most one link per record
	InverseOf string `json:"inverse_of,omitempty"` // set on the generated reverse field: the forward field id
}
type AutonumberConfig struct {
	Prefix string `json:"prefix"` // e.g. "ORD-"
}

func ConfigFor(t FieldType) any {
	switch t {
	case Number, Percent, Currency:
		return &NumberConfig{}
	case SingleSelect, MultiSelect:
		return &SelectConfig{}
	case User:
		return &UserConfig{}
	case Link:
		return &LinkConfig{}
	case Autonumber:
		return &AutonumberConfig{}
	}
	return nil
}

type Base struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"-"`
	SpaceID     string `json:"space_id"`
	Name        string `json:"name"`
}
type Table struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"-"`
	BaseID      string `json:"base_id"`
	Name        string `json:"name"`
}
type Field struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"-"`
	TableID     string          `json:"table_id"`
	Name        string          `json:"name"`
	Type        FieldType       `json:"type"`
	Config      json.RawMessage `json:"config"`
	Position    int             `json:"position"`
	Locked      bool            `json:"locked"`
}

// Coerce and Validate live in coerce.go (package 1).

var errNotImplemented = command.NewError(command.Internal, "tables: not implemented yet")

// Query shapes shared by record.list, views.config and the query compiler (package 2).
type Rule struct {
	Field string `json:"field"` // field id; checked against the table's fields
	Op    string `json:"op"`    // see design section 4
	Value any    `json:"value,omitempty"`
}
type Filter struct {
	Match string `json:"match"` // "and" | "or"
	Rules []Rule `json:"rules"` // at most 10, one level
}
type Sort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// Cursor is the keyset page token: the last row's sort values, in the order of the request's sorts (nil = empty),
// and the record id, which is always the final tie-break. The token is opaque to clients and only ever bound as
// parameters, so a forged one can at worst select a different page of rows the caller may already read.
type Cursor struct {
	Sort []any  `json:"s"`
	ID   string `json:"id"`
}

func (c Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (Cursor, error) {
	var c Cursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err != nil || c.ID == "" {
		return Cursor{}, errors.New("tables: invalid cursor")
	}
	return c, nil
}
