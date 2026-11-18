// Package reports is the custom report builder: a static registry of datasets (one row grain each, only
// many-to-one joins so sums never multiply), a compiler from a JSON Spec to parameterised SQL, and the
// report.* / dashboard.* commands. Identifiers come only from the registry; every user value is a bind.
package reports

import "time"

// Spec is what the builder sends and what core.reports.spec stores. It is re-validated on every run
// with the caller's rights.
type Spec struct {
	Dataset  string    `json:"dataset"`
	Columns  []string  `json:"columns"` // row mode (no group_by): fields to show, in order
	Filters  []Filter  `json:"filters"` // ANDed
	Period   *Period   `json:"period"`
	GroupBy  []Group   `json:"group_by"` // v1: at most 1
	Measures []Measure `json:"measures"` // group mode: at most 6; empty means [{agg:count}]
	Sort     []Sort    `json:"sort"`
	Limit    int       `json:"limit"` // 0 = default; capped at MaxRows
	Viz      string    `json:"viz"`   // table | bar | number
}

type Filter struct {
	Field string `json:"field"`
	Op    string `json:"op"`    // see OpsByType
	Value any    `json:"value"` // string | number | bool | []string | [from,to]
}

// Period resolves server-side (IST, workspace FY start month) to an inclusive [From, To] date range on Field.
type Period struct {
	Field  string `json:"field"`
	Preset string `json:"preset"` // this_month last_month this_quarter last_quarter this_fy last_fy fy_to_date last_12_months custom
	From   string `json:"from"`   // YYYY-MM-DD, custom only
	To     string `json:"to"`
}

type Group struct {
	Field  string `json:"field"`
	Bucket string `json:"bucket"` // date fields only: day week month quarter fy ("" = day)
}

type Measure struct {
	Field string `json:"field"` // empty for count
	Agg   string `json:"agg"`   // count sum avg min max
}

type Sort struct {
	Key string `json:"key"` // a column field key, "g0", or "m0".."m5"
	Dir string `json:"dir"` // asc | desc
}

// Field types. money is stored in minor units (paise) and returned as integers.
const (
	TText    = "text"
	TEnum    = "enum"
	TDate    = "date"
	TMoney   = "money"
	TNumber  = "number"
	TBool    = "bool"
	TPerson  = "person"  // core.users id, label = display name
	TContact = "contact" // crm.contacts id, label = name
)

var OpsByType = map[string][]string{
	TText:    {"eq", "neq", "contains", "empty", "not_empty"},
	TEnum:    {"in", "not_in"},
	TDate:    {"between", "before", "after", "empty", "not_empty"},
	TMoney:   {"eq", "gt", "gte", "lt", "lte", "between"},
	TNumber:  {"eq", "gt", "gte", "lt", "lte", "between"},
	TBool:    {"is"},
	TPerson:  {"in", "not_in", "empty", "not_empty", "me"},
	TContact: {"in", "not_in", "empty", "not_empty"},
}

const (
	MaxRows     = 2000  // on screen
	MaxCSVRows  = 10000 // export
	MaxGroups   = 1
	MaxMeasures = 6
	MaxFilters  = 20
	MaxColumns  = 20
	TimeoutMS   = 5000 // statement_timeout, set transaction-local before running
)

// Env is everything about the caller the compiler needs. Built once per command from the Ctx.
type Env struct {
	Spaces       []string          // access.ReadableSpaceIDs
	UserID       *string           // access.Viewer
	Admin        bool              // access.Viewer
	Role         int               // command.Role as int (0 guest .. 3 owner)
	Finance      bool              // c.FinanceAllowed()
	ModuleOn     func(string) bool // settings.modules switch ("billing", "crm", "projects", ...)
	FYStartMonth int               // company fy_start_month, default 4
	Today        time.Time         // billing.TodayIST()
}

// FieldInfo / DatasetInfo are what report.datasets returns (only what the caller may use).
type FieldInfo struct {
	Key    string   `json:"key"`
	Label  string   `json:"label"`
	Type   string   `json:"type"`
	Aggs   []string `json:"aggs"`             // allowed measures on this field (sum/avg/min/max); count is always allowed
	Values []Option `json:"values,omitempty"` // enum values
	Group  bool     `json:"group"`            // may be grouped by
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type DatasetInfo struct {
	Key         string      `json:"key"`
	Label       string      `json:"label"`       // owner words: "Invoices", "Time logged"
	Description string      `json:"description"` // one line: what one row is
	DefaultDate string      `json:"default_date"`
	DefaultCols []string    `json:"default_columns"`
	Fields      []FieldInfo `json:"fields"`
}

// Column describes one output column of a run.
type Column struct {
	Key   string `json:"key"`   // field key, "g0", or "m0".."m5"
	Label string `json:"label"` // "Customer", "Total amount", "Number of invoices"
	Type  string `json:"type"`
}

// Query is the compiled statement. Rows: SQL with Args, at most Limit+1 rows (the extra row means truncated).
// Totals: one row of the same measures over the full filtered population (nil in row mode without money/number
// columns). Count: total matching rows before any limit.
type Query struct {
	SQL       string
	Args      []any
	Columns   []Column
	Limit     int
	TotalsSQL string
	CountSQL  string // same Args
	From, To  string // resolved period, "" when none
	PeriodLbl string // "This FY (1 Apr 2026 – 31 Mar 2027)"
}

// Contract implemented in datasets.go / compile.go / dates.go:
//
//	func Catalog(env Env) []DatasetInfo
//	func Compile(spec Spec, env Env, limit int) (Query, error)  // returns *command.Error (Invalid/Forbidden) on bad specs
//	func ResolvePeriod(p Period, fyStartMonth int, today time.Time) (from, to time.Time, label string, err error)
//
// Run result JSON (report.run):
//
//	{"columns":[Column...], "rows":[[...]...], "totals":[...]|null, "count":N, "truncated":bool,
//	 "period":{"from":"2026-04-01","to":"2027-03-31","label":"This FY"}|null}
//
// Row cells: text/enum/date as strings (enum = value; labels come from the catalog), money/number as numbers,
// person/contact as {"id":"…","label":"…"} (null when empty). In group mode the g0 cell for a date bucket is the
// bucket start date "YYYY-MM-DD" and the measure cells are numbers.
