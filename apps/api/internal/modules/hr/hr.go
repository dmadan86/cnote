// Package hr is attendance, leave and holidays (WP10). No payroll and no salary. An employee is a people record: a
// workspace member (created on first use) or an offline employee without a login. Days are IST calendar days.
// Leave types, quotas and holidays are workspace data seeded with editable defaults, not statutory tables: state
// Shops and Establishments rules differ, so the defaults are not legal advice. Go-only.
package hr

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

func Commands() []command.Command {
	mk := func(name string, g command.Group, min command.Role, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole = min
		return c
	}
	const (
		M, A = command.Member, command.Admin
		R, W = command.GroupRead, command.GroupWriteNotes
		adm  = command.GroupAdmin
	)
	register := mk("attendance.register", R, A, register)
	register.Confirm = true // bulk personal data: a machine caller needs a human to confirm
	return []command.Command{
		mk("attendance.check_in", W, M, checkIn),
		mk("attendance.check_out", W, M, checkOut),
		mk("attendance.mark", adm, A, mark),
		mk("attendance.today", R, M, todayBoard),
		register,
		mk("leave.request", W, M, leaveRequest),
		mk("leave.decide", W, M, leaveDecide),
		mk("leave.cancel", W, M, leaveCancel),
		mk("leave.balance", R, M, leaveBalance),
		mk("leave.list", R, M, leaveList),
		mk("leave_type.list", R, M, leaveTypeList),
		mk("leave_type.set", adm, A, leaveTypeSet),
		mk("holiday.list", R, M, holidayList),
		mk("holiday.set", adm, A, holidaySet),
		mk("holiday.delete", adm, A, holidayDelete),
		mk("employee.list", R, A, employeeList),
		mk("employee.create", adm, A, employeeCreate),
		mk("employee.update", adm, A, employeeUpdate),
	}
}

// ---------- input helpers ----------

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type obj map[string]json.RawMessage

func decode(raw json.RawMessage) (obj, error) {
	var m obj
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrInvalid("input must be a JSON object")
	}
	return m, nil
}

func (o obj) has(k string) bool { _, ok := o[k]; return ok }

// str: absent and null both give "" (use has to tell them apart).
func (o obj) str(k string, max int) (string, error) {
	b, ok := o[k]
	if !ok || string(b) == "null" {
		return "", nil
	}
	var v string
	if json.Unmarshal(b, &v) != nil {
		return "", command.ErrInvalid(k + " must be a string")
	}
	if v = strings.TrimSpace(v); len([]rune(v)) > max {
		return "", command.ErrInvalid(k + " is too long")
	}
	return v, nil
}

func (o obj) id(k string, required bool) (string, error) {
	v, err := o.str(k, 36)
	if err != nil {
		return "", err
	}
	if v = strings.ToLower(v); v == "" && required || v != "" && !uuidRe.MatchString(v) {
		return "", command.ErrInvalid(k + " must be a UUID")
	}
	return v, nil
}

func (o obj) date(k string, required bool) (string, error) {
	v, err := o.str(k, 10)
	if err != nil {
		return "", err
	}
	if v == "" && required {
		return "", command.ErrInvalid(k + " is required")
	}
	if v != "" {
		if _, e := time.Parse(time.DateOnly, v); e != nil {
			return "", command.ErrInvalid(k + " must be a date like 2026-04-01")
		}
	}
	return v, nil
}

func (o obj) boolean(k string) (v bool, present bool, err error) {
	b, ok := o[k]
	if !ok {
		return false, false, nil
	}
	if json.Unmarshal(b, &v) != nil {
		return false, true, command.ErrInvalid(k + " must be true or false")
	}
	return v, true, nil
}

func (o obj) number(k string) (v float64, present bool, err error) {
	b, ok := o[k]
	if !ok || string(b) == "null" {
		return 0, false, nil
	}
	if json.Unmarshal(b, &v) != nil || v < 0 || v > 366 {
		return 0, true, command.ErrInvalid(k + " must be a number from 0 to 366")
	}
	return v, true, nil
}

func human(c *command.Ctx) (string, error) {
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return "", command.ErrForbidden("only people can do this, not API keys or agents")
	}
	return *c.Principal.UserID, nil
}

func isAdmin(c *command.Ctx) bool { return c.Principal.Role >= command.Admin }

func today(c *command.Ctx) (string, error) {
	var d string
	err := c.Tx.QueryRow(c.Context, "select (now() at time zone 'Asia/Kolkata')::date::text").Scan(&d)
	return d, command.DB(err)
}

// ---------- defaults, employees ----------

// Editable defaults: common practice, not legal advice; check the customer's state rules (Shops and Establishments).
var defaultLeaveTypes = []struct {
	name  string
	quota float64
}{{"Casual leave", 12}, {"Sick leave", 12}, {"Earned leave", 15}}

// shortcut: only the three national holidays are seeded; the owner adds festival holidays by hand, since dates
// move every year and the statutory minimum differs by state. Upgrade: a suggested list per state, verified.
var nationalHolidays = []struct {
	month, day int
	name       string
}{{1, 26, "Republic Day"}, {8, 15, "Independence Day"}, {10, 2, "Gandhi Jayanti"}}

// seed inserts the default leave types once per workspace and the national holidays once per year; the marker is
// settings.hr_seeded, so deleting a default is not undone.
func seed(c *command.Ctx, year int) error {
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select coalesce(settings->'hr_seeded','[]'::jsonb) from core.workspaces where id=$1::uuid", c.WorkspaceID()).Scan(&raw); err != nil {
		return command.DB(err)
	}
	var done []string
	_ = json.Unmarshal(raw, &done)
	has := func(k string) bool {
		for _, d := range done {
			if d == k {
				return true
			}
		}
		return false
	}
	hk := "h" + time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006")
	changed := false
	if !has("types") {
		for _, t := range defaultLeaveTypes {
			if _, err := c.Tx.Exec(c.Context, "insert into hr.leave_types (workspace_id, name, yearly_quota) values ($1::uuid,$2,$3) on conflict do nothing", c.WorkspaceID(), t.name, t.quota); err != nil {
				return command.DB(err)
			}
		}
		done, changed = append(done, "types"), true
	}
	if !has(hk) {
		for _, h := range nationalHolidays {
			d := time.Date(year, time.Month(h.month), h.day, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
			if _, err := c.Tx.Exec(c.Context, "insert into hr.holidays (workspace_id, day, name, kind) values ($1::uuid,$2::date,$3,'national') on conflict do nothing", c.WorkspaceID(), d, h.name); err != nil {
				return command.DB(err)
			}
		}
		done, changed = append(done, hk), true
	}
	if !changed {
		return nil
	}
	b, _ := json.Marshal(done)
	_, err := c.Tx.Exec(c.Context, "update core.workspaces set settings = jsonb_set(settings, '{hr_seeded}', $2::jsonb) where id=$1::uuid", c.WorkspaceID(), string(b))
	return command.DB(err)
}

func seedFor(c *command.Ctx, day string) error {
	t, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return command.ErrInvalid("bad date")
	}
	return seed(c, t.Year())
}

// ensureEmployees (safe when two requests race) gives every active non-guest member an employee record.
func ensureEmployees(c *command.Ctx) error {
	_, err := c.Tx.Exec(c.Context, `insert into hr.employees (workspace_id, user_id, name)
		select m.workspace_id, m.user_id, coalesce(nullif(btrim(u.name),''), u.email) from core.memberships m join core.users u on u.id = m.user_id
		where m.workspace_id = $1::uuid and m.deactivated_at is null and m.role <> 'guest'
		  and not exists (select 1 from hr.employees e where e.workspace_id = m.workspace_id and e.user_id = m.user_id)
		on conflict do nothing`, c.WorkspaceID())
	return command.DB(err)
}

func selfEmployee(c *command.Ctx) (string, error) {
	uid, err := human(c)
	if err != nil {
		return "", err
	}
	if err := ensureEmployees(c); err != nil {
		return "", err
	}
	var id string
	var active bool
	err = c.Tx.QueryRow(c.Context, "select id::text, active from hr.employees where user_id = $1::uuid", uid).Scan(&id, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("your employee record")
	}
	if err != nil {
		return "", command.DB(err)
	}
	if !active {
		return "", command.ErrConflict("your employee record is inactive")
	}
	return id, nil
}

// scope is the people the caller may read: everyone for an admin, otherwise themselves and their direct reports.
// Arguments $1 = caller user, $2 = admin.
const scope = `($2::bool or e.user_id = $1::uuid or exists (select 1 from core.memberships r where r.workspace_id = e.workspace_id and r.user_id = e.user_id and r.manager_user_id = $1::uuid))`

// inWindow: the employee counts on a day when they have no login, or an active membership whose joined/left dates cover it.
const inWindow = `(e.user_id is null or exists (select 1 from core.memberships m where m.workspace_id = e.workspace_id and m.user_id = e.user_id
	and m.deactivated_at is null and (m.joined_on is null or m.joined_on <= %s::date) and (m.left_on is null or m.left_on >= %s::date)))`

func inWindowAt(p string) string { return strings.ReplaceAll(inWindow, "%s", p) }

// canSee: may the caller read this employee's attendance and leave.
func canSee(c *command.Ctx, emp string) (bool, error) {
	var ok bool
	err := c.Tx.QueryRow(c.Context, "select exists (select 1 from hr.employees e where e.id = $3::uuid and "+scope+")", c.Principal.UserID, isAdmin(c), emp).Scan(&ok)
	return ok, command.DB(err)
}

func employeeRow(c *command.Ctx, id string) (map[string]any, error) {
	rows, err := employees(c, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, command.ErrNotFound("employee")
	}
	return rows[0], nil
}

func employees(c *command.Ctx, only string) ([]map[string]any, error) {
	rows, err := c.Tx.Query(c.Context, `select e.id::text, e.user_id::text, coalesce(u.name, e.name), u.email, e.code, e.weekly_off, e.active
		from hr.employees e left join core.users u on u.id = e.user_id where ($1 = '' or e.id = $1::uuid)
		order by (not e.active), coalesce(u.name, e.name)`, only)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var uid, email, code *string
		var off []int32
		var active bool
		if err := rows.Scan(&id, &uid, &name, &email, &code, &off, &active); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "user_id": uid, "name": name, "email": email, "code": code, "weekly_off": off, "active": active})
	}
	return out, command.DB(rows.Err())
}

func employeeList(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	if err := ensureEmployees(c); err != nil {
		return nil, err
	}
	return employees(c, "")
}

func weeklyOff(o obj) ([]int, bool, error) {
	b, ok := o["weekly_off"]
	if !ok {
		return nil, false, nil
	}
	var off []int
	if json.Unmarshal(b, &off) != nil || len(off) > 6 {
		return nil, true, command.ErrInvalid("weekly_off is a list of weekdays, 0 = Sunday to 6 = Saturday")
	}
	for _, d := range off {
		if d < 0 || d > 6 {
			return nil, true, command.ErrInvalid("weekly_off is a list of weekdays, 0 = Sunday to 6 = Saturday")
		}
	}
	if off == nil {
		off = []int{}
	}
	return off, true, nil
}

// employeeCreate adds an offline employee (no login). Members get their record automatically.
func employeeCreate(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	name, err := o.str("name", 120)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, command.ErrInvalid("name is required")
	}
	code, err := o.str("code", 30)
	if err != nil {
		return nil, err
	}
	off, has, err := weeklyOff(o)
	if err != nil {
		return nil, err
	}
	if !has {
		off = []int{0}
	}
	var id string
	if err := c.Tx.QueryRow(c.Context, "insert into hr.employees (workspace_id, name, code, weekly_off) values ($1::uuid,$2,nullif($3,''),$4) returning id::text", c.WorkspaceID(), name, code, off).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("employee.created", nil, map[string]any{"id": id}); err != nil {
		return nil, err
	}
	return employeeRow(c, id)
}

func employeeUpdate(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, err := o.id("id", true)
	if err != nil {
		return nil, err
	}
	var hasUser bool
	if err := c.Tx.QueryRow(c.Context, "select user_id is not null from hr.employees where id=$1::uuid", id).Scan(&hasUser); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("employee")
	} else if err != nil {
		return nil, command.DB(err)
	}
	name, err := o.str("name", 120)
	if err != nil {
		return nil, err
	}
	if o.has("name") && hasUser {
		return nil, command.ErrInvalid("a member's name comes from their account")
	}
	if o.has("name") && name == "" {
		return nil, command.ErrInvalid("name is required")
	}
	code, err := o.str("code", 30)
	if err != nil {
		return nil, err
	}
	off, hasOff, err := weeklyOff(o)
	if err != nil {
		return nil, err
	}
	active, hasActive, err := o.boolean("active")
	if err != nil {
		return nil, err
	}
	if !o.has("name") && !o.has("code") && !hasOff && !hasActive {
		return nil, command.ErrInvalid("nothing to update")
	}
	if _, err := c.Tx.Exec(c.Context, `update hr.employees set name = case when $2 then $3 else name end, code = case when $4 then nullif($5,'') else code end,
		weekly_off = case when $6 then $7::int[] else weekly_off end, active = case when $8 then $9 else active end where id = $1::uuid`,
		id, o.has("name"), name, o.has("code"), code, hasOff, off, hasActive, active); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("employee.updated", nil, map[string]any{"id": id}); err != nil {
		return nil, err
	}
	return employeeRow(c, id)
}

// ---------- attendance ----------

func attendanceRow(c *command.Ctx, emp, day string) (map[string]any, error) {
	var id, status string
	var in, out *time.Time
	var note *string
	err := c.Tx.QueryRow(c.Context, "select id::text, status, in_at, out_at, note from hr.attendance where employee_id=$1::uuid and day=$2::date", emp, day).Scan(&id, &status, &in, &out, &note)
	if err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"id": id, "employee_id": emp, "day": day, "status": status, "in_at": command.TsPtr(in), "out_at": command.TsPtr(out), "note": note}, nil
}

func checkIn(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	emp, err := selfEmployee(c)
	if err != nil {
		return nil, err
	}
	day, err := today(c)
	if err != nil {
		return nil, err
	}
	var already bool
	if err := c.Tx.QueryRow(c.Context, "select exists (select 1 from hr.attendance where employee_id=$1::uuid and day=$2::date and in_at is not null)", emp, day).Scan(&already); err != nil {
		return nil, command.DB(err)
	}
	if already {
		return attendanceRow(c, emp, day)
	}
	// An admin-marked absence or an approved-leave row becomes present; the leave stays counted against the balance.
	if _, err := c.Tx.Exec(c.Context, `insert into hr.attendance (workspace_id, employee_id, day, status, in_at, marked_by) values ($1::uuid,$2::uuid,$3::date,'present',now(),$4::uuid)
		on conflict (workspace_id, employee_id, day) do update set status = 'present', in_at = now(), out_at = null, marked_by = excluded.marked_by`, c.WorkspaceID(), emp, day, c.Principal.UserID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("attendance.marked", nil, map[string]any{"employee_id": emp, "day": day, "status": "present", "via": "check_in"}); err != nil {
		return nil, err
	}
	return attendanceRow(c, emp, day)
}

func checkOut(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	emp, err := selfEmployee(c)
	if err != nil {
		return nil, err
	}
	day, err := today(c)
	if err != nil {
		return nil, err
	}
	// today's row, or yesterday's still open (a shift that ran past midnight)
	var rowDay string
	err = c.Tx.QueryRow(c.Context, `select day::text from hr.attendance where employee_id=$1::uuid and in_at is not null
		and (day = $2::date or (day = $2::date - 1 and out_at is null)) order by day desc limit 1`, emp, day).Scan(&rowDay)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrConflict("check in first")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "update hr.attendance set out_at = now() where employee_id=$1::uuid and day=$2::date", emp, rowDay); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("attendance.marked", nil, map[string]any{"employee_id": emp, "day": rowDay, "status": "present", "via": "check_out"}); err != nil {
		return nil, err
	}
	return attendanceRow(c, emp, rowDay)
}

var markable = map[string]bool{"present": true, "half_day": true, "absent": true, "week_off": true, "holiday": true}

func mark(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	emp, err := o.id("employee_id", true)
	if err != nil {
		return nil, err
	}
	day, err := o.date("day", true)
	if err != nil {
		return nil, err
	}
	status, err := o.str("status", 20)
	if err != nil {
		return nil, err
	}
	if !markable[status] {
		return nil, command.ErrInvalid("status must be present, half_day, absent, week_off or holiday (leave comes from approved leave)")
	}
	note, err := o.str("note", 300)
	if err != nil {
		return nil, err
	}
	now, err := today(c)
	if err != nil {
		return nil, err
	}
	var okDay bool // the current IST month and the one before; never the future
	if err := c.Tx.QueryRow(c.Context, "select $1::date <= $2::date and $1::date >= (date_trunc('month', $2::date) - interval '1 month')::date", day, now).Scan(&okDay); err != nil {
		return nil, command.DB(err)
	}
	if !okDay {
		return nil, command.ErrInvalid("attendance can be marked for this month and the previous month, not for the future")
	}
	var onLeave bool
	err = c.Tx.QueryRow(c.Context, "select coalesce((select status = 'leave' from hr.attendance where employee_id=$1::uuid and day=$2::date), false) from hr.employees where id=$1::uuid", emp, day).Scan(&onLeave)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("employee")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if onLeave {
		return nil, command.ErrConflict("that day is approved leave; cancel the leave first")
	}
	if _, err := c.Tx.Exec(c.Context, `insert into hr.attendance (workspace_id, employee_id, day, status, marked_by, note) values ($1::uuid,$2::uuid,$3::date,$4,$5::uuid,nullif($6,''))
		on conflict (workspace_id, employee_id, day) do update set status = excluded.status, marked_by = excluded.marked_by, note = excluded.note`,
		c.WorkspaceID(), emp, day, status, c.Principal.UserID, note); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("attendance.marked", nil, map[string]any{"employee_id": emp, "day": day, "status": status, "via": "admin"}); err != nil {
		return nil, err
	}
	return attendanceRow(c, emp, day)
}

// Counts of a day's board. Expected leaves out people on a holiday or weekly off.
type Counts struct {
	Expected  int `json:"expected"`
	Present   int `json:"present"`
	OnLeave   int `json:"on_leave"`
	Absent    int `json:"absent"`
	NotMarked int `json:"not_marked"`
}

func board(c *command.Ctx, day string, all bool) ([]map[string]any, Counts, error) {
	var n Counts
	rows, err := c.Tx.Query(c.Context, `select e.id::text, coalesce(u.name, e.name), e.code, e.user_id::text,
		coalesce(a.status, case when exists (select 1 from hr.holidays h where h.day = $3::date) then 'holiday'
			when e.weekly_off @> array[extract(dow from $3::date)::int] then 'week_off' else 'not_marked' end), a.in_at, a.out_at
		from hr.employees e left join core.users u on u.id = e.user_id left join hr.attendance a on a.employee_id = e.id and a.day = $3::date
		where e.active and `+inWindowAt("$3")+` and `+scope+` order by coalesce(u.name, e.name)`, c.Principal.UserID, all, day)
	if err != nil {
		return nil, n, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, st string
		var code, uid *string
		var in, outAt *time.Time
		if err := rows.Scan(&id, &name, &code, &uid, &st, &in, &outAt); err != nil {
			return nil, n, command.DB(err)
		}
		if st != "holiday" && st != "week_off" {
			n.Expected++
		}
		switch st {
		case "present", "half_day":
			n.Present++
		case "leave":
			n.OnLeave++
		case "absent":
			n.Absent++
		case "not_marked":
			n.NotMarked++
		}
		out = append(out, map[string]any{"employee_id": id, "user_id": uid, "name": name, "code": code, "status": st, "in_at": command.TsPtr(in), "out_at": command.TsPtr(outAt)})
	}
	return out, n, command.DB(rows.Err())
}

// PeoplePresent feeds the owner home tile: everyone, so the caller must be an admin.
func PeoplePresent(c *command.Ctx) (Counts, error) {
	if err := ensureEmployees(c); err != nil {
		return Counts{}, err
	}
	day, err := today(c)
	if err != nil {
		return Counts{}, err
	}
	if err := seedFor(c, day); err != nil {
		return Counts{}, err
	}
	_, n, err := board(c, day, true)
	return n, err
}

func todayBoard(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	if err := ensureEmployees(c); err != nil {
		return nil, err
	}
	day, err := today(c)
	if err != nil {
		return nil, err
	}
	if err := seedFor(c, day); err != nil {
		return nil, err
	}
	people, counts, err := board(c, day, isAdmin(c))
	if err != nil {
		return nil, err
	}
	var me any
	for _, p := range people {
		if u, _ := p["user_id"].(*string); u != nil && c.Principal.UserID != nil && *u == *c.Principal.UserID {
			me = p
		}
	}
	return map[string]any{"day": day, "counts": counts, "people": people, "me": me}, nil
}
