package hr

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

// ---------- leave types and holidays (workspace data) ----------

func leaveTypeList(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	d, err := today(c)
	if err != nil {
		return nil, err
	}
	if err := seedFor(c, d); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select id::text, name, yearly_quota::float8, paid, active from hr.leave_types order by (not active), name")
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var q *float64
		var paid, active bool
		if err := rows.Scan(&id, &name, &q, &paid, &active); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "name": name, "yearly_quota": q, "paid": paid, "active": active})
	}
	return out, command.DB(rows.Err())
}

// leaveTypeSet creates a type (no id) or updates one. yearly_quota null = no limit. Types are deactivated, not
// deleted, because requests refer to them.
func leaveTypeSet(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	if _, err := leaveTypeList(c, json.RawMessage(`{}`)); err != nil { // seed defaults before the first edit
		return nil, err
	}
	id, err := o.id("id", false)
	if err != nil {
		return nil, err
	}
	name, err := o.str("name", 60)
	if err != nil {
		return nil, err
	}
	if (id == "" || o.has("name")) && name == "" {
		return nil, command.ErrInvalid("name is required")
	}
	var quota *float64
	if o.has("yearly_quota") && string(o["yearly_quota"]) != "null" {
		q, _, err := o.number("yearly_quota")
		if err != nil {
			return nil, err
		}
		quota = &q
	}
	paid, hasPaid, err := o.boolean("paid")
	if err != nil {
		return nil, err
	}
	active, hasActive, err := o.boolean("active")
	if err != nil {
		return nil, err
	}
	if id == "" {
		if !hasPaid {
			paid = true
		}
		err = c.Tx.QueryRow(c.Context, "insert into hr.leave_types (workspace_id, name, yearly_quota, paid) values ($1::uuid,$2,$3,$4) returning id::text", c.WorkspaceID(), name, quota, paid).Scan(&id)
	} else {
		var n int
		err = c.Tx.QueryRow(c.Context, `with u as (update hr.leave_types set name = case when $2 then $3 else name end, yearly_quota = case when $4 then $5::numeric else yearly_quota end,
			paid = case when $6 then $7 else paid end, active = case when $8 then $9 else active end where id = $1::uuid returning 1) select count(*) from u`,
			id, o.has("name"), name, o.has("yearly_quota"), quota, hasPaid, paid, hasActive, active).Scan(&n)
		if err == nil && n == 0 {
			return nil, command.ErrNotFound("leave type")
		}
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("leave_type.updated", nil, map[string]any{"id": id}); err != nil {
		return nil, err
	}
	var out map[string]any
	for _, r := range mustList(c) {
		if r["id"] == id {
			out = r
		}
	}
	return out, nil
}

func mustList(c *command.Ctx) []map[string]any {
	v, _ := leaveTypeList(c, json.RawMessage(`{}`))
	l, _ := v.([]map[string]any)
	return l
}

func holidayList(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	d, err := today(c)
	if err != nil {
		return nil, err
	}
	year := d[:4]
	if o.has("year") {
		var y int
		if json.Unmarshal(o["year"], &y) != nil || y < 2000 || y > 2100 {
			return nil, command.ErrInvalid("year must be like 2026")
		}
		year = strconv.Itoa(y)
	}
	y, _ := strconv.Atoi(year)
	if err := seed(c, y); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, "select id::text, day::text, name, kind from hr.holidays where extract(year from day) = $1::int order by day", y)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, day, name, kind string
		if err := rows.Scan(&id, &day, &name, &kind); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "day": day, "name": name, "kind": kind})
	}
	return out, command.DB(rows.Err())
}

// holidaySet adds a holiday or renames the one on that day.
func holidaySet(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	day, err := o.date("day", true)
	if err != nil {
		return nil, err
	}
	name, err := o.str("name", 80)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, command.ErrInvalid("name is required")
	}
	kind, err := o.str("kind", 10)
	if err != nil {
		return nil, err
	}
	if kind == "" {
		kind = "company"
	}
	if kind != "festival" && kind != "company" {
		return nil, command.ErrInvalid("kind must be festival or company (the national holidays are seeded)")
	}
	if err := seedFor(c, day); err != nil {
		return nil, err
	}
	var id string
	if err := c.Tx.QueryRow(c.Context, `insert into hr.holidays (workspace_id, day, name, kind) values ($1::uuid,$2::date,$3,$4)
		on conflict (workspace_id, day) do update set name = excluded.name, kind = case when hr.holidays.kind = 'national' then 'national' else excluded.kind end returning id::text`,
		c.WorkspaceID(), day, name, kind).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("holiday.set", nil, map[string]any{"id": id, "day": day}); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "day": day, "name": name}, nil
}

func holidayDelete(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, err := o.id("id", true)
	if err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context, "delete from hr.holidays where id=$1::uuid", id)
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, command.ErrNotFound("holiday")
	}
	if _, err := c.Emit("holiday.deleted", nil, map[string]any{"id": id}); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// ---------- leave ----------

// workingDays counts the days of the range that are neither the employee's weekly off nor a holiday.
func workingDays(c *command.Ctx, emp, from, to string) (int, error) {
	var n int
	err := c.Tx.QueryRow(c.Context, `select count(*) from generate_series($2::date, $3::date, interval '1 day') g(d)
		where not (select weekly_off from hr.employees where id = $1::uuid) @> array[extract(dow from g.d)::int]
		and not exists (select 1 from hr.holidays h where h.day = g.d::date)`, emp, from, to).Scan(&n)
	return n, command.DB(err)
}

func leaveRequest(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	typeID, err := o.id("leave_type_id", true)
	if err != nil {
		return nil, err
	}
	from, err := o.date("from_day", true)
	if err != nil {
		return nil, err
	}
	to, err := o.date("to_day", false)
	if err != nil {
		return nil, err
	}
	if to == "" {
		to = from
	}
	half, _, err := o.boolean("half_day")
	if err != nil {
		return nil, err
	}
	reason, err := o.str("reason", 500)
	if err != nil {
		return nil, err
	}
	emp, err := o.id("employee_id", false)
	if err != nil {
		return nil, err
	}
	var me *string
	if emp == "" {
		if emp, err = selfEmployee(c); err != nil {
			return nil, err
		}
		me = c.Principal.UserID
	} else {
		if !isAdmin(c) {
			return nil, command.ErrForbidden("only an admin can request leave for someone else")
		}
		me = c.Principal.UserID
	}
	if to < from {
		return nil, command.ErrInvalid("to_day is before from_day")
	}
	if from[:4] != to[:4] {
		return nil, command.ErrInvalid("a request cannot span two calendar years; send one per year")
	}
	if half && from != to {
		return nil, command.ErrInvalid("a half day is a single day")
	}
	now, err := today(c)
	if err != nil {
		return nil, err
	}
	var recent bool
	if err := c.Tx.QueryRow(c.Context, "select $1::date >= (date_trunc('month', $2::date) - interval '1 month')::date", from, now).Scan(&recent); err != nil {
		return nil, command.DB(err)
	}
	if !recent {
		return nil, command.ErrInvalid("leave cannot start before last month")
	}
	var active bool // locks the employee so two requests cannot both pass the balance check
	if err := c.Tx.QueryRow(c.Context, "select active from hr.employees where id=$1::uuid for update", emp).Scan(&active); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("employee")
	} else if err != nil {
		return nil, command.DB(err)
	}
	if !active {
		return nil, command.ErrConflict("that employee is inactive")
	}
	if err := seedFor(c, from); err != nil {
		return nil, err
	}
	var quota *float64
	var typeActive bool
	if err := c.Tx.QueryRow(c.Context, "select yearly_quota::float8, active from hr.leave_types where id=$1::uuid", typeID).Scan(&quota, &typeActive); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrInvalid("leave type not found")
	} else if err != nil {
		return nil, command.DB(err)
	}
	if !typeActive {
		return nil, command.ErrInvalid("that leave type is switched off")
	}
	n, err := workingDays(c, emp, from, to)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, command.ErrInvalid("those dates are all weekly offs or holidays; no leave is needed")
	}
	days := float64(n)
	if half {
		days = 0.5
	}
	var overlap bool
	if err := c.Tx.QueryRow(c.Context, `select exists (select 1 from hr.leave_requests where employee_id=$1::uuid and status in ('pending','approved') and from_day <= $3::date and to_day >= $2::date)`, emp, from, to).Scan(&overlap); err != nil {
		return nil, command.DB(err)
	}
	if overlap {
		return nil, command.ErrConflict("there is already a pending or approved leave request on those dates")
	}
	if quota != nil {
		var used float64
		if err := c.Tx.QueryRow(c.Context, `select coalesce(sum(days),0)::float8 from hr.leave_requests where employee_id=$1::uuid and leave_type_id=$2::uuid
			and status in ('pending','approved') and extract(year from from_day) = extract(year from $3::date)`, emp, typeID, from).Scan(&used); err != nil {
			return nil, command.DB(err)
		}
		if used+days > *quota {
			return nil, command.ErrConflict(fmt.Sprintf("not enough balance: %.1f day(s) left of this leave type, %.1f asked", *quota-used, days))
		}
	}
	var id string
	if err := c.Tx.QueryRow(c.Context, `insert into hr.leave_requests (workspace_id, employee_id, leave_type_id, from_day, to_day, days, half_day, reason, requested_by)
		values ($1::uuid,$2::uuid,$3::uuid,$4::date,$5::date,$6,$7,nullif($8,''),$9::uuid) returning id::text`, c.WorkspaceID(), emp, typeID, from, to, days, half, reason, me).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("leave.requested", nil, map[string]any{"id": id, "employee_id": emp, "from_day": from, "to_day": to, "days": days}); err != nil {
		return nil, err
	}
	return leaveRow(c, id)
}

const leaveSelect = `select r.id::text, r.employee_id::text, coalesce(u.name, e.name), t.name, r.from_day::text, r.to_day::text, r.days::float8, r.half_day, r.status,
	r.reason, r.decision_note, du.name, r.created_at,
	(r.status = 'pending' and (($2::bool and (e.user_id is distinct from $1::uuid or $3::bool)) or (e.user_id is distinct from $1::uuid and exists
		(select 1 from core.memberships m where m.workspace_id = e.workspace_id and m.user_id = e.user_id and m.manager_user_id = $1::uuid)))),
	(r.status in ('pending','approved') and (e.user_id = $1::uuid or $2::bool))
	from hr.leave_requests r join hr.employees e on e.id = r.employee_id join hr.leave_types t on t.id = r.leave_type_id
	left join core.users u on u.id = e.user_id left join core.users du on du.id = r.decided_by `

func scanLeave(rows pgx.Rows) (map[string]any, error) {
	var id, emp, name, typ, from, to, status string
	var days float64
	var half, canDecide, canCancel bool
	var reason, note, decider *string
	var at time.Time
	if err := rows.Scan(&id, &emp, &name, &typ, &from, &to, &days, &half, &status, &reason, &note, &decider, &at, &canDecide, &canCancel); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"id": id, "employee_id": emp, "employee": name, "leave_type": typ, "from_day": from, "to_day": to, "days": days, "half_day": half,
		"status": status, "reason": reason, "decision_note": note, "decided_by": decider, "created_at": command.Ts(at), "can_decide": canDecide, "can_cancel": canCancel}, nil
}

func leaveArgs(c *command.Ctx) []any {
	return []any{c.Principal.UserID, isAdmin(c), c.Principal.Role == command.Owner}
}

func leaveRow(c *command.Ctx, id string) (any, error) {
	rows, err := c.Tx.Query(c.Context, leaveSelect+"where r.id = $4::uuid", append(leaveArgs(c), id)...)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, command.ErrNotFound("leave request")
	}
	return scanLeave(rows)
}

// leaveList: your own requests, those of your direct reports, and for an admin everyone's.
func leaveList(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	status, err := o.str("status", 10)
	if err != nil {
		return nil, err
	}
	emp, err := o.id("employee_id", false)
	if err != nil {
		return nil, err
	}
	if err := ensureEmployees(c); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, leaveSelect+`where `+scope+` and ($4 = '' or r.status = $4) and ($5 = '' or r.employee_id = $5::uuid)
		order by r.from_day desc, r.created_at desc limit 200`, append(leaveArgs(c), status, emp)...)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		m, err := scanLeave(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, command.DB(rows.Err())
}

type request struct {
	id, emp, status, from, to, empUser string
	half                               bool
}

func loadRequest(c *command.Ctx, id string) (request, error) {
	r := request{id: id}
	var uid *string
	err := c.Tx.QueryRow(c.Context, `select r.employee_id::text, r.status, r.from_day::text, r.to_day::text, r.half_day, e.user_id::text
		from hr.leave_requests r join hr.employees e on e.id = r.employee_id where r.id = $1::uuid for update of r`, id).Scan(&r.emp, &r.status, &r.from, &r.to, &r.half, &uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, command.ErrNotFound("leave request")
	}
	if uid != nil {
		r.empUser = *uid
	}
	return r, command.DB(err)
}

func leaveDecide(c *command.Ctx, b json.RawMessage) (any, error) {
	me, err := human(c)
	if err != nil {
		return nil, err
	}
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, err := o.id("id", true)
	if err != nil {
		return nil, err
	}
	decision, err := o.str("decision", 10)
	if err != nil {
		return nil, err
	}
	reason, err := o.str("reason", 500)
	if err != nil {
		return nil, err
	}
	if decision != "approve" && decision != "reject" {
		return nil, command.ErrInvalid("decision must be approve or reject")
	}
	if decision == "reject" && reason == "" {
		return nil, command.ErrInvalid("a reason is required to reject")
	}
	r, err := loadRequest(c, id)
	if err != nil {
		return nil, err
	}
	var reports bool
	if r.empUser != "" {
		if err := c.Tx.QueryRow(c.Context, "select exists (select 1 from core.memberships where user_id=$1::uuid and manager_user_id=$2::uuid)", r.empUser, me).Scan(&reports); err != nil {
			return nil, command.DB(err)
		}
	}
	if r.empUser == me && c.Principal.Role != command.Owner {
		return nil, command.ErrForbidden("you cannot decide your own leave")
	}
	if !isAdmin(c) && !reports {
		return nil, command.ErrNotFound("leave request") // not yours to see
	}
	if r.status != "pending" {
		return nil, command.ErrConflict("this request is already " + r.status)
	}
	st := "rejected"
	if decision == "approve" {
		st = "approved"
		if err := writeLeaveDays(c, r, me); err != nil {
			return nil, err
		}
	}
	if _, err := c.Tx.Exec(c.Context, "update hr.leave_requests set status=$2, decided_by=$3::uuid, decided_at=now(), decision_note=nullif($4,'') where id=$1::uuid", id, st, me, reason); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("leave."+st, nil, map[string]any{"id": id, "employee_id": r.emp}); err != nil {
		return nil, err
	}
	return leaveRow(c, id)
}

// leaveNotes mark the attendance rows an approval wrote, so a cancel removes those and nothing a person recorded.
const leaveNote, halfNote = "Leave", "Half-day leave"

func writeLeaveDays(c *command.Ctx, r request, by string) error {
	status, note := "leave", leaveNote
	if r.half {
		status, note = "half_day", halfNote
	}
	_, err := c.Tx.Exec(c.Context, `insert into hr.attendance (workspace_id, employee_id, day, status, marked_by, note)
		select $1::uuid, $2::uuid, g.d::date, $5, $4::uuid, $6 from generate_series($3::date, $7::date, interval '1 day') g(d)
		where not (select weekly_off from hr.employees where id = $2::uuid) @> array[extract(dow from g.d)::int]
		and not exists (select 1 from hr.holidays h where h.day = g.d::date)
		on conflict (workspace_id, employee_id, day) do update set status = excluded.status, note = excluded.note, marked_by = excluded.marked_by
		where hr.attendance.in_at is null`, c.WorkspaceID(), r.emp, r.from, by, status, note, r.to)
	return command.DB(err)
}

func leaveCancel(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, err := o.id("id", true)
	if err != nil {
		return nil, err
	}
	r, err := loadRequest(c, id)
	if err != nil {
		return nil, err
	}
	mine := r.empUser != "" && c.Principal.UserID != nil && r.empUser == *c.Principal.UserID
	if !mine && !isAdmin(c) {
		return nil, command.ErrNotFound("leave request")
	}
	if r.status != "pending" && r.status != "approved" {
		return nil, command.ErrConflict("this request is already " + r.status)
	}
	now, err := today(c)
	if err != nil {
		return nil, err
	}
	if r.status == "approved" && r.to < now && !isAdmin(c) {
		return nil, command.ErrConflict("leave that has already passed can only be cancelled by an admin")
	}
	if r.status == "approved" {
		if _, err := c.Tx.Exec(c.Context, `delete from hr.attendance where employee_id=$1::uuid and day between $2::date and $3::date and in_at is null
			and ((status = 'leave' and note = $4) or (status = 'half_day' and note = $5))`, r.emp, r.from, r.to, leaveNote, halfNote); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Tx.Exec(c.Context, "update hr.leave_requests set status='cancelled' where id=$1::uuid", id); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("leave.cancelled", nil, map[string]any{"id": id, "employee_id": r.emp}); err != nil {
		return nil, err
	}
	return leaveRow(c, id)
}

func leaveBalance(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	emp, err := o.id("employee_id", false)
	if err != nil {
		return nil, err
	}
	if err := ensureEmployees(c); err != nil {
		return nil, err
	}
	if emp == "" {
		if emp, err = selfEmployee(c); err != nil {
			return nil, err
		}
	} else if ok, err := canSee(c, emp); err != nil {
		return nil, err
	} else if !ok {
		return nil, command.ErrNotFound("employee")
	}
	d, err := today(c)
	if err != nil {
		return nil, err
	}
	year := d[:4]
	if o.has("year") {
		var y int
		if json.Unmarshal(o["year"], &y) != nil || y < 2000 || y > 2100 {
			return nil, command.ErrInvalid("year must be like 2026")
		}
		year = strconv.Itoa(y)
	}
	if err := seedFor(c, year+"-01-01"); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select t.id::text, t.name, t.paid, t.yearly_quota::float8,
		coalesce(sum(r.days) filter (where r.status = 'approved'), 0)::float8, coalesce(sum(r.days) filter (where r.status = 'pending'), 0)::float8
		from hr.leave_types t left join hr.leave_requests r on r.leave_type_id = t.id and r.employee_id = $1::uuid and extract(year from r.from_day) = $2::int
		where t.active or r.id is not null group by t.id order by t.name`, emp, year)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var paid bool
		var quota *float64
		var used, pending float64
		if err := rows.Scan(&id, &name, &paid, &quota, &used, &pending); err != nil {
			return nil, command.DB(err)
		}
		var left any
		if quota != nil {
			left = *quota - used - pending
		}
		out = append(out, map[string]any{"leave_type_id": id, "name": name, "paid": paid, "yearly_quota": quota, "used": used, "pending": pending, "remaining": left})
	}
	return out, command.DB(rows.Err())
}

// ---------- monthly register ----------

var cell = map[string]string{"present": "P", "half_day": "HD", "absent": "A", "leave": "L", "holiday": "H", "week_off": "WO"}

// csvSafe stops a name from turning into a spreadsheet formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func register(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	month, err := o.str("month", 7)
	if err != nil {
		return nil, err
	}
	start, perr := time.Parse("2006-01", month)
	if perr != nil {
		return nil, command.ErrInvalid("month must be like 2026-04")
	}
	if err := ensureEmployees(c); err != nil {
		return nil, err
	}
	if err := seed(c, start.Year()); err != nil {
		return nil, err
	}
	now, err := today(c)
	if err != nil {
		return nil, err
	}
	nd := start.AddDate(0, 1, -1).Day()
	first, last := start.Format(time.DateOnly), start.AddDate(0, 1, -1).Format(time.DateOnly)

	hol := map[int]bool{}
	hq, err := c.Tx.Query(c.Context, "select extract(day from day)::int from hr.holidays where day between $1::date and $2::date", first, last)
	if err != nil {
		return nil, command.DB(err)
	}
	for hq.Next() {
		var d int
		if err := hq.Scan(&d); err != nil {
			hq.Close()
			return nil, command.DB(err)
		}
		hol[d] = true
	}
	hq.Close()

	type emp struct {
		id, name, code string
		off            []int32
		days           []string
	}
	var list []*emp
	byID := map[string]*emp{}
	er, err := c.Tx.Query(c.Context, `select e.id::text, coalesce(u.name, e.name), coalesce(e.code,''), e.weekly_off from hr.employees e left join core.users u on u.id = e.user_id
		where e.active or exists (select 1 from hr.attendance a where a.employee_id = e.id and a.day between $1::date and $2::date) order by coalesce(e.code,''), coalesce(u.name, e.name)`, first, last)
	if err != nil {
		return nil, command.DB(err)
	}
	for er.Next() {
		e := &emp{days: make([]string, nd)}
		if err := er.Scan(&e.id, &e.name, &e.code, &e.off); err != nil {
			er.Close()
			return nil, command.DB(err)
		}
		list, byID[e.id] = append(list, e), e
	}
	er.Close()
	if err := er.Err(); err != nil {
		return nil, command.DB(err)
	}
	ar, err := c.Tx.Query(c.Context, "select employee_id::text, extract(day from day)::int, status from hr.attendance where day between $1::date and $2::date", first, last)
	if err != nil {
		return nil, command.DB(err)
	}
	for ar.Next() {
		var id, st string
		var d int
		if err := ar.Scan(&id, &d, &st); err != nil {
			ar.Close()
			return nil, command.DB(err)
		}
		if e := byID[id]; e != nil {
			e.days[d-1] = cell[st]
		}
	}
	ar.Close()
	if err := ar.Err(); err != nil {
		return nil, command.DB(err)
	}

	var sb strings.Builder
	w := csv.NewWriter(&sb)
	head := []string{"Code", "Name"}
	for d := 1; d <= nd; d++ {
		head = append(head, strconv.Itoa(d))
	}
	head = append(head, "Present days", "Half days", "Leave days", "Absent days", "Holidays", "Weekly offs", "Not marked")
	_ = w.Write(head)
	rows := []map[string]any{}
	for _, e := range list {
		var p, hd, l, a, h, wo, nm int
		for d := 1; d <= nd; d++ {
			day := start.AddDate(0, 0, d-1)
			dateStr := day.Format(time.DateOnly)
			if e.days[d-1] == "" { // no record: a holiday or weekly off by calendar, else unmarked once the day has come
				switch {
				case hol[d]:
					e.days[d-1] = "H"
				case offDay(e.off, day.Weekday()):
					e.days[d-1] = "WO"
				}
			}
			switch e.days[d-1] {
			case "P":
				p++
			case "HD":
				hd++
			case "L":
				l++
			case "A":
				a++
			case "H":
				h++
			case "WO":
				wo++
			case "":
				if dateStr <= now {
					nm++
				}
			}
		}
		rows = append(rows, map[string]any{"employee_id": e.id, "name": e.name, "code": e.code, "days": e.days,
			"totals": map[string]int{"present": p, "half_day": hd, "leave": l, "absent": a, "holiday": h, "week_off": wo, "not_marked": nm}})
		rec := append([]string{csvSafe(e.code), csvSafe(e.name)}, e.days...)
		for _, n := range []int{p, hd, l, a, h, wo, nm} {
			rec = append(rec, strconv.Itoa(n))
		}
		_ = w.Write(rec)
	}
	w.Flush()
	if _, err := c.Emit("attendance.register_read", nil, map[string]any{"month": month, "rows": len(rows)}); err != nil {
		return nil, err
	}
	return map[string]any{"month": month, "days_in_month": nd, "legend": "P present, HD half day, A absent, L leave, H holiday, WO weekly off, blank not marked", "rows": rows, "csv": sb.String(), "filename": "attendance-" + month + ".csv"}, nil
}

func offDay(off []int32, d time.Weekday) bool {
	for _, o := range off {
		if int(o) == int(d) {
			return true
		}
	}
	return false
}
