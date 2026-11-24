package reports

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func Commands() []command.Command {
	nc := func(name string, g command.Group, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole = command.Member // guests never run reports, even the read ones
		c.Confirm = g == command.GroupDestructive
		return c
	}
	return []command.Command{
		nc("report.datasets", command.GroupRead, datasets),
		nc("report.run", command.GroupRead, run),
		nc("report.run_many", command.GroupRead, runMany),
		nc("report.get", command.GroupRead, reportGet),
		nc("report.list", command.GroupRead, reportList),
		nc("report.save", command.GroupWriteNotes, reportSave),
		nc("report.delete", command.GroupDestructive, reportDelete),
		nc("dashboard.get", command.GroupRead, dashboardGet),
		nc("dashboard.list", command.GroupRead, dashboardList),
		nc("dashboard.save", command.GroupWriteNotes, dashboardSave),
		nc("dashboard.delete", command.GroupDestructive, dashboardDelete),
	}
}

func decode(input json.RawMessage, v any) error {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if err := json.Unmarshal(input, v); err != nil {
		return command.ErrInvalid("invalid input: " + err.Error())
	}
	return nil
}

func datasets(c *command.Ctx, input json.RawMessage) (any, error) {
	env, err := buildEnv(c)
	if err != nil {
		return nil, err
	}
	return map[string]any{"datasets": Catalog(env)}, nil
}

// ---------- saved reports ----------

type report struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Dataset    string          `json:"dataset"`
	Spec       json.RawMessage `json:"spec"`
	Visibility string          `json:"visibility"`
	CreatedBy  string          `json:"created_by"`
	OwnerName  string          `json:"owner_name"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	CanEdit    bool            `json:"can_edit"`
}

const reportCols = `r.id::text, r.name, r.dataset, r.spec, r.visibility, r.created_by::text, u.name, r.created_at, r.updated_at`

func uidOf(c *command.Ctx) string {
	if c.Principal.UserID == nil {
		return ""
	}
	return *c.Principal.UserID
}

func scanReport(c *command.Ctx, row pgx.Row) (report, error) {
	var r report
	if err := row.Scan(&r.ID, &r.Name, &r.Dataset, &r.Spec, &r.Visibility, &r.CreatedBy, &r.OwnerName, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return r, err
	}
	// Private is owner only, for admins too: the object is hidden before can_edit matters.
	r.CanEdit = r.CreatedBy == uidOf(c) || (c.Principal.Role >= command.Admin && r.Visibility == "workspace")
	return r, nil
}

func visible(c *command.Ctx, owner, vis string) bool {
	return vis == "workspace" || owner == uidOf(c)
}

// loadReport returns a report the caller may see; anything else (missing, other workspace, someone's private) is not found.
func loadReport(c *command.Ctx, id string) (report, error) {
	if !isUUID(id) {
		return report{}, command.ErrNotFound("report")
	}
	r, err := scanReport(c, c.Tx.QueryRow(c.Context, `select `+reportCols+` from core.reports r join core.users u on u.id = r.created_by
		where r.id = $1::uuid and r.workspace_id = $2::uuid`, id, c.WorkspaceID()))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !visible(c, r.CreatedBy, r.Visibility)) {
		return report{}, command.ErrNotFound("report")
	}
	return r, command.DB(err)
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, ch := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if ch != '-' {
				return false
			}
		case !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F'):
			return false
		}
	}
	return true
}

type idIn struct {
	ID string `json:"id"`
}

func reportGet(c *command.Ctx, input json.RawMessage) (any, error) {
	var in idIn
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	r, err := loadReport(c, in.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"report": r}, nil
}

func reportList(c *command.Ctx, input json.RawMessage) (any, error) {
	rows, err := c.Tx.Query(c.Context, `select `+reportCols+` from core.reports r join core.users u on u.id = r.created_by
		where r.workspace_id = $1::uuid and (r.visibility = 'workspace' or r.created_by = $2::uuid) order by r.updated_at desc, r.id`, c.WorkspaceID(), nilIfEmpty(uidOf(c)))
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []report{}
	for rows.Next() {
		r, err := scanReport(c, rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, r)
	}
	return map[string]any{"reports": out}, command.DB(rows.Err())
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n < 1 || n > 120 {
		return "", command.ErrInvalid("name is 1 to 120 characters")
	}
	return name, nil
}

func checkVisibility(v string) (string, error) {
	if v == "" {
		return "private", nil
	}
	if v != "private" && v != "workspace" {
		return "", command.ErrInvalid("visibility is private or workspace")
	}
	return v, nil
}

func needPerson(c *command.Ctx) error {
	if c.Principal.UserID == nil {
		return command.ErrForbidden("saving needs a signed-in person")
	}
	return nil
}

func reportSave(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Spec       Spec   `json:"spec"`
		Visibility string `json:"visibility"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	if err := needPerson(c); err != nil {
		return nil, err
	}
	name, err := checkName(in.Name)
	if err != nil {
		return nil, err
	}
	vis, err := checkVisibility(in.Visibility)
	if err != nil {
		return nil, err
	}
	env, err := buildEnv(c)
	if err != nil {
		return nil, err
	}
	// A report you cannot run is not one you can save.
	if _, err := Compile(in.Spec, env, 0); err != nil {
		return nil, err
	}
	spec, _ := json.Marshal(in.Spec)
	id := in.ID
	if id == "" {
		err = c.Tx.QueryRow(c.Context, `insert into core.reports (workspace_id, name, dataset, spec, visibility, created_by)
			values ($1::uuid, $2, $3, $4, $5, $6::uuid) returning id::text`, c.WorkspaceID(), name, in.Spec.Dataset, spec, vis, uidOf(c)).Scan(&id)
		if err != nil {
			return nil, command.DB(err)
		}
	} else {
		old, err := loadReport(c, id)
		if err != nil {
			return nil, err
		}
		if !old.CanEdit {
			return nil, command.ErrForbidden("only the owner or an admin can change this report")
		}
		if _, err := c.Tx.Exec(c.Context, `update core.reports set name=$2, dataset=$3, spec=$4, visibility=$5, updated_at=now() where id=$1::uuid`,
			id, name, in.Spec.Dataset, spec, vis); err != nil {
			return nil, command.DB(err)
		}
	}
	r, err := loadReport(c, id)
	return map[string]any{"report": r}, err
}

func reportDelete(c *command.Ctx, input json.RawMessage) (any, error) {
	var in idIn
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	r, err := loadReport(c, in.ID)
	if err != nil {
		return nil, err
	}
	if !r.CanEdit {
		return nil, command.ErrForbidden("only the owner or an admin can delete this report")
	}
	if _, err := c.Tx.Exec(c.Context, "delete from core.reports where id = $1::uuid", r.ID); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"ok": true}, nil
}

// ---------- run ----------

type runIn struct {
	Spec   *Spec   `json:"spec"`
	ID     string  `json:"id"`
	Period *Period `json:"period"`
	Format string  `json:"format"`
}

const (
	runBurst  = 20 // report.run bucket; report.run_many draws from the same one
	runRefill = 2
)

func runBucket(c *command.Ctx) string { return "report.run:" + c.WorkspaceID() + ":" + uidOf(c) }

func run(c *command.Ctx, input json.RawMessage) (any, error) {
	var in runIn
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	if in.Format != "" && in.Format != "csv" {
		return nil, command.ErrInvalid("format is csv or omitted")
	}
	if err := command.CheckRateWith(runBucket(c), runBurst, runRefill); err != nil {
		return nil, err
	}
	env, err := buildEnv(c)
	if err != nil {
		return nil, err
	}
	return runOne(c, env, in)
}

// runOne is the whole of report.run after the rate limit: visibility, readable spaces, module state, finance,
// period, limits and timeout. report.run_many calls it once per run with the caller's own Env.
func runOne(c *command.Ctx, env Env, in runIn) (any, error) {
	var spec Spec
	switch {
	case in.ID != "":
		r, err := loadReport(c, in.ID)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(r.Spec, &spec); err != nil {
			return nil, command.ErrInvalid("saved report is unreadable")
		}
	case in.Spec != nil:
		spec = *in.Spec
	default:
		return nil, command.ErrInvalid("spec or id is required")
	}
	if in.Period != nil {
		p := *in.Period
		if p.Field == "" {
			if spec.Period != nil {
				p.Field = spec.Period.Field
			} else {
				for _, d := range Catalog(env) {
					if d.Key == spec.Dataset {
						p.Field = d.DefaultDate
					}
				}
			}
		}
		spec.Period = &p
	}
	limit := 0 // the spec's own limit, capped at MaxRows
	if in.Format == "csv" {
		limit = MaxCSVRows
	}
	q, err := Compile(spec, env, limit)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "select set_config('statement_timeout', $1, true)", strconv.Itoa(TimeoutMS)); err != nil {
		return nil, command.DB(err)
	}
	rows, truncated, err := fetch(c, q)
	if err != nil {
		return nil, err
	}
	if in.Format == "csv" {
		return toCSV(q, rows, truncated), nil
	}
	var totals []any
	if q.TotalsSQL != "" {
		tr, err := c.Tx.Query(c.Context, q.TotalsSQL, q.Args...)
		if err != nil {
			return nil, queryErr(err)
		}
		if tr.Next() {
			vals, err := tr.Values()
			if err != nil {
				tr.Close()
				return nil, command.DB(err)
			}
			totals = normRow(vals)
		}
		tr.Close()
		if err := tr.Err(); err != nil {
			return nil, queryErr(err)
		}
	}
	var count int64
	if err := c.Tx.QueryRow(c.Context, q.CountSQL, q.Args...).Scan(&count); err != nil {
		return nil, queryErr(err)
	}
	var period any
	if q.From != "" {
		period = map[string]string{"from": q.From, "to": q.To, "label": q.PeriodLbl}
	}
	return map[string]any{"columns": q.Columns, "rows": rows, "totals": totals, "count": count, "truncated": truncated, "period": period}, nil
}

// queryErr turns the statement timeout into advice, anything else into a database error.
func queryErr(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "57014" {
		return command.ErrInvalid("this report took too long; narrow the dates or add a filter")
	}
	return command.DB(err)
}

// fetch runs the rows query; the compiler asks for Limit+1 rows, the extra one only signals truncation.
func fetch(c *command.Ctx, q Query) ([][]any, bool, error) {
	rs, err := c.Tx.Query(c.Context, q.SQL, q.Args...)
	if err != nil {
		return nil, false, queryErr(err)
	}
	defer rs.Close()
	out := [][]any{}
	for rs.Next() {
		vals, err := rs.Values()
		if err != nil {
			return nil, false, command.DB(err)
		}
		out = append(out, normRow(vals))
	}
	if err := rs.Err(); err != nil {
		return nil, false, queryErr(err)
	}
	truncated := len(out) > q.Limit
	if truncated {
		out = out[:q.Limit]
	}
	return out, truncated, nil
}

// normRow turns driver values into JSON cells (numeric to numbers; money/number/date already arrive as bigint/float8/text).
func normRow(vals []any) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = normCell(v)
	}
	return out
}

func normCell(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case pgtype.Numeric:
		if !x.Valid {
			return nil
		}
		f, err := x.Float64Value()
		if err != nil || !f.Valid {
			return nil
		}
		if f.Float64 == math.Trunc(f.Float64) && math.Abs(f.Float64) < 1<<53 {
			return int64(f.Float64)
		}
		return f.Float64
	case time.Time:
		return x.Format("2006-01-02")
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:])
	}
	return v
}

// ---------- CSV ----------

func rupees(minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
}

// csvSafe stops a text value from turning into a spreadsheet formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func csvCell(typ string, v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case map[string]any:
		l, _ := x["label"].(string)
		return csvSafe(l)
	case string:
		return csvSafe(x)
	case bool:
		return strconv.FormatBool(x)
	}
	if typ == TMoney {
		switch x := v.(type) {
		case int64:
			return rupees(x)
		case int32:
			return rupees(int64(x))
		case float64:
			return rupees(int64(math.Round(x)))
		}
	}
	return fmt.Sprint(v)
}

func toCSV(q Query, rows [][]any, truncated bool) map[string]any {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	head := make([]string, len(q.Columns))
	for i, col := range q.Columns {
		head[i] = csvSafe(col.Label)
	}
	_ = w.Write(head)
	for _, r := range rows {
		rec := make([]string, len(q.Columns))
		for i, col := range q.Columns {
			if i < len(r) {
				rec[i] = csvCell(col.Type, r[i])
			}
		}
		_ = w.Write(rec)
	}
	w.Flush()
	if truncated {
		fmt.Fprintf(&sb, "# truncated at %d rows\n", q.Limit)
	}
	return map[string]any{"csv": sb.String(), "filename": "report-" + time.Now().UTC().Format("2006-01-02") + ".csv"}
}
