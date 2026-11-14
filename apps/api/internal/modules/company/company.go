// Package company is the foundation other modules read: the legal profile that prints on documents (flat keys in
// workspaces.settings, the same ones billing already reads), departments, and the people directory with reporting
// line, deactivation and the finance-access flag. Go-only.
package company

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/jackc/pgx/v5"
)

func Commands() []command.Command {
	nc := command.NewCommand
	admin := func(c command.Command) command.Command { c.MinRole = command.Admin; return c }
	member := func(c command.Command) command.Command { c.MinRole = command.Member; return c }
	del := nc("department.delete", command.GroupDestructive, departmentDelete)
	del.Confirm = true
	return []command.Command{
		member(nc("company.get", command.GroupRead, companyGet)),
		admin(nc("company.update", command.GroupAdmin, companyUpdate)),
		member(nc("department.list", command.GroupRead, departmentList)),
		admin(nc("department.create", command.GroupAdmin, departmentCreate)),
		admin(nc("department.update", command.GroupAdmin, departmentUpdate)),
		admin(del),
		member(nc("people.list", command.GroupRead, peopleList)),
		admin(nc("member.update", command.GroupAdmin, memberUpdate)),
		admin(nc("member.deactivate", command.GroupAdmin, memberDeactivate)),
		admin(nc("member.reactivate", command.GroupAdmin, memberReactivate)),
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// in keeps null apart from absent: null clears a field, absent leaves it.
type in map[string]json.RawMessage

func decode(raw json.RawMessage) (in, error) {
	var m in
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrInvalid("input must be a JSON object")
	}
	return m, nil
}

func (m in) has(k string) bool { _, ok := m[k]; return ok }

// str returns the trimmed value; present is false when absent, and "" with present=true means null/empty (clear).
func (m in) str(k string, max int) (v string, present bool, err error) {
	b, ok := m[k]
	if !ok {
		return "", false, nil
	}
	if string(b) == "null" {
		return "", true, nil
	}
	if json.Unmarshal(b, &v) != nil {
		return "", true, command.ErrInvalid(k + " must be a string")
	}
	v = strings.TrimSpace(v)
	if len([]rune(v)) > max {
		return "", true, command.ErrInvalid(k + " is too long")
	}
	return v, true, nil
}

func (m in) id(k string, required bool) (string, bool, error) {
	v, ok, err := m.str(k, 36)
	if err != nil {
		return "", ok, err
	}
	v = strings.ToLower(v)
	if v == "" && required || v != "" && !uuidRe.MatchString(v) {
		return "", ok, command.ErrInvalid(k + " must be a UUID")
	}
	return v, ok, nil
}

func (m in) date(k string) (string, bool, error) {
	v, ok, err := m.str(k, 10)
	if err == nil && v != "" {
		if _, e := time.Parse(time.DateOnly, v); e != nil {
			err = command.ErrInvalid(k + " must be a date like 2026-04-01")
		}
	}
	return v, ok, err
}

func human(c *command.Ctx) error {
	if c.Principal.Kind != command.KindUser {
		return command.ErrForbidden("only people can do this, not API keys or agents")
	}
	return nil
}

// ---------- company profile ----------

// Flat keys of workspaces.settings, shared with the billing settings form and invoice printing.
var profileKeys = []string{"legal_name", "trade_name", "pan", "gstin", "state_code", "address", "pincode", "phone", "email",
	"upi_vpa", "upi_payee_name", "bank_name", "bank_account", "bank_ifsc", "bank_details"}

func settingsOf(c *command.Ctx) (map[string]any, string, error) {
	var raw []byte
	var name string
	if err := c.Tx.QueryRow(c.Context, "select settings, name from core.workspaces where id=$1::uuid", c.WorkspaceID()).Scan(&raw, &name); err != nil {
		return nil, "", command.DB(err)
	}
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m, name, nil
}

func companyGet(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	s, name, err := settingsOf(c)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"name": name, "fy_start_month": 4}
	for _, k := range profileKeys {
		v, _ := s[k].(string)
		out[k] = v
	}
	if f, ok := s["fy_start_month"].(float64); ok {
		out["fy_start_month"] = int(f)
	}
	return out, nil
}

var checks = map[string]struct {
	re   *regexp.Regexp
	max  int
	what string
}{
	"pan": {panRe, 10, "PAN looks like ABCDE1234F"}, "pincode": {pinRe, 6, "PIN code is 6 digits"},
	"bank_ifsc": {ifscRe, 11, "IFSC looks like HDFC0001234"}, "bank_account": {acctRe, 18, "bank account is 9 to 18 digits"},
	"upi_vpa": {upiRe, 100, "UPI ID looks like name@bank"}, "email": {emailRe, 120, "email is not valid"},
}

func companyUpdate(c *command.Ctx, b json.RawMessage) (any, error) {
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	cur, _, err := settingsOf(c)
	if err != nil {
		return nil, err
	}
	set, clear := map[string]any{}, []string{}
	val := func(k string) (string, bool) { // value after this update
		if v, ok := set[k].(string); ok {
			return v, true
		}
		if slices.Contains(clear, k) {
			return "", false
		}
		v, ok := cur[k].(string)
		return v, ok && v != ""
	}
	maxLen := map[string]int{"legal_name": 120, "trade_name": 120, "address": 400, "upi_payee_name": 80, "bank_name": 80, "bank_details": 300, "phone": 20, "gstin": 15, "state_code": 2}
	for _, k := range profileKeys {
		max := maxLen[k]
		if ck, ok := checks[k]; ok {
			max = ck.max + 20
		}
		v, present, err := m.str(k, max)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		switch k {
		case "pan", "bank_ifsc", "gstin":
			v = upperTrim(v)
		case "phone":
			v = cleanPhone(v)
		case "bank_account":
			v = strings.ReplaceAll(v, " ", "")
		}
		if v == "" {
			clear = append(clear, k)
			continue
		}
		switch k {
		case "gstin":
			if v, err = billing.ValidateGSTIN(v); err != nil {
				return nil, err
			}
		case "phone":
			if !phoneRe.MatchString(v) {
				return nil, command.ErrInvalid("phone is 7 to 15 digits, optionally starting with +")
			}
		case "state_code":
			if len(v) != 2 || v[0] < '0' || v[0] > '9' || v[1] < '0' || v[1] > '9' {
				return nil, command.ErrInvalid("state_code is the 2-digit GST state code")
			}
		default:
			if ck, ok := checks[k]; ok && !ck.re.MatchString(v) {
				return nil, command.ErrInvalid(ck.what)
			}
		}
		set[k] = v
	}
	if f, ok := m["fy_start_month"]; ok {
		var n int
		if json.Unmarshal(f, &n) != nil || n < 1 || n > 12 {
			return nil, command.ErrInvalid("fy_start_month is 1 to 12")
		}
		set["fy_start_month"] = n
	}
	// GSTIN carries the state and the PAN: derive the state, and refuse a PAN or state that contradicts it.
	if g, ok := val("gstin"); ok {
		if st, ok := val("state_code"); ok && st != billing.GSTINState(g) {
			if _, given, _ := m.str("state_code", 2); given {
				return nil, command.ErrInvalid("state_code " + st + " does not match the GSTIN state " + billing.GSTINState(g))
			}
		}
		if _, given, _ := m.str("gstin", 15); given {
			set["state_code"] = billing.GSTINState(g)
			clear = slices.DeleteFunc(clear, func(k string) bool { return k == "state_code" })
		}
		if pan, ok := val("pan"); ok && g[2:12] != pan {
			return nil, command.ErrInvalid("PAN " + pan + " does not match the PAN inside the GSTIN")
		}
	}
	// invoices print bank_details: compose it from the structured fields unless the caller wrote it
	if _, given := set["bank_details"]; !given && !slices.Contains(clear, "bank_details") {
		_, a := set["bank_account"]
		_, i := set["bank_ifsc"]
		_, n := set["bank_name"]
		if a || i || n {
			bn, _ := val("bank_name")
			ba, _ := val("bank_account")
			bi, _ := val("bank_ifsc")
			var parts []string
			if bn != "" {
				parts = append(parts, bn)
			}
			if ba != "" {
				parts = append(parts, "A/c "+ba)
			}
			if bi != "" {
				parts = append(parts, "IFSC "+bi)
			}
			set["bank_details"] = strings.Join(parts, ", ")
		}
	}
	patch, _ := json.Marshal(set)
	if _, err := c.Tx.Exec(c.Context, "update core.workspaces set settings = (settings - $2::text[]) || $3::jsonb where id=$1::uuid", c.WorkspaceID(), clear, patch); err != nil {
		return nil, command.DB(err)
	}
	keys := make([]string, 0, len(set)+len(clear))
	for k := range set {
		keys = append(keys, k)
	}
	keys = append(keys, clear...)
	slices.Sort(keys)
	if _, err := c.Emit("company.updated", nil, map[string]any{"keys": keys}); err != nil {
		return nil, err
	}
	return companyGet(c, json.RawMessage(`{}`))
}

// ---------- departments ----------

func departmentList(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select d.id::text, d.name, d.head_user_id::text, u.name,
		(select count(*) from core.memberships m where m.department_id = d.id and m.deactivated_at is null)
		from core.departments d left join core.users u on u.id = d.head_user_id order by d.name`)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var head, headName *string
		var n int64
		if err := rows.Scan(&id, &name, &head, &headName, &n); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"id": id, "name": name, "head_user_id": head, "head_name": headName, "members": n})
	}
	return out, command.DB(rows.Err())
}

func activeMember(c *command.Ctx, uid string) error {
	var ok bool
	if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from core.memberships where user_id=$1::uuid and deactivated_at is null)", uid).Scan(&ok); err != nil {
		return command.DB(err)
	}
	if !ok {
		return command.ErrInvalid("that person is not an active member of this workspace")
	}
	return nil
}

func deptName(c *command.Ctx, m in, id string) (string, bool, error) {
	name, present, err := m.str("name", 80)
	if err != nil || !present {
		return "", present, err
	}
	if name == "" {
		return "", true, command.ErrInvalid("name is required")
	}
	var taken bool
	if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from core.departments where name=$1 and ($2 = '' or id <> $2::uuid))", name, id).Scan(&taken); err != nil {
		return "", true, command.DB(err)
	}
	if taken {
		return "", true, command.ErrConflict("a department named " + name + " already exists")
	}
	return name, true, nil
}

func departmentCreate(c *command.Ctx, b json.RawMessage) (any, error) {
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	name, present, err := deptName(c, m, "")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, command.ErrInvalid("name is required")
	}
	head, hasHead, err := m.id("head_user_id", false)
	if err != nil {
		return nil, err
	}
	if hasHead && head != "" {
		if err := activeMember(c, head); err != nil {
			return nil, err
		}
	}
	var id string
	if err := c.Tx.QueryRow(c.Context, "insert into core.departments (workspace_id, name, head_user_id) values ($1::uuid,$2,nullif($3,'')::uuid) returning id::text", c.WorkspaceID(), name, head).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	_, err = c.Emit("department.created", nil, map[string]any{"id": id, "name": name})
	return map[string]any{"id": id, "name": name, "head_user_id": nilIfEmpty(head)}, err
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func departmentUpdate(c *command.Ctx, b json.RawMessage) (any, error) {
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("id", true)
	if err != nil {
		return nil, err
	}
	name, hasName, err := deptName(c, m, id)
	if err != nil {
		return nil, err
	}
	head, hasHead, err := m.id("head_user_id", false)
	if err != nil {
		return nil, err
	}
	if hasHead && head != "" {
		if err := activeMember(c, head); err != nil {
			return nil, err
		}
	}
	var gotName string
	var gotHead *string
	err = c.Tx.QueryRow(c.Context, `update core.departments set name = case when $2 then $3 else name end,
		head_user_id = case when $4 then nullif($5,'')::uuid else head_user_id end where id=$1::uuid returning name, head_user_id::text`,
		id, hasName, name, hasHead, head).Scan(&gotName, &gotHead)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("department")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	_, err = c.Emit("department.updated", nil, map[string]any{"id": id})
	return map[string]any{"id": id, "name": gotName, "head_user_id": gotHead}, err
}

func departmentDelete(c *command.Ctx, b json.RawMessage) (any, error) {
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, _, err := m.id("id", true)
	if err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context, "delete from core.departments where id=$1::uuid", id) // members are unassigned by the FK
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() == 0 {
		return nil, command.ErrNotFound("department")
	}
	_, err = c.Emit("department.deleted", nil, map[string]any{"id": id})
	return map[string]any{"deleted": true}, err
}

// ---------- people ----------

func peopleList(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select m.user_id::text, u.email, u.name, m.role, m.department_id::text, d.name,
		m.manager_user_id::text, mu.name, m.designation, m.phone, m.joined_on::text, m.left_on::text, m.finance_access, m.deactivated_at
		from core.memberships m join core.users u on u.id = m.user_id
		left join core.departments d on d.id = m.department_id
		left join core.users mu on mu.id = m.manager_user_id
		order by (m.deactivated_at is not null), u.name`)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var uid, email, name, role string
		var dep, depName, mgr, mgrName, desig, phone, joined, left *string
		var fin bool
		var deact *time.Time
		if err := rows.Scan(&uid, &email, &name, &role, &dep, &depName, &mgr, &mgrName, &desig, &phone, &joined, &left, &fin, &deact); err != nil {
			return nil, command.DB(err)
		}
		var d any
		if deact != nil {
			d = command.Ts(*deact)
		}
		out = append(out, map[string]any{"user_id": uid, "email": email, "name": name, "role": role, "department_id": dep, "department": depName,
			"manager_user_id": mgr, "manager_name": mgrName, "designation": desig, "phone": phone, "joined_on": joined, "left_on": left,
			"finance_access": fin, "deactivated_at": d})
	}
	return out, command.DB(rows.Err())
}

type target struct {
	role   command.Role
	active bool
}

func loadTarget(c *command.Ctx, uid string) (target, error) {
	var role string
	var deact *time.Time
	err := c.Tx.QueryRow(c.Context, "select role, deactivated_at from core.memberships where user_id=$1::uuid", uid).Scan(&role, &deact)
	if errors.Is(err, pgx.ErrNoRows) {
		return target{}, command.ErrNotFound("member")
	}
	return target{command.ParseRole(role), deact == nil}, command.DB(err)
}

// outranked is the existing member.add rule: nobody changes an owner, and only an owner changes a peer admin.
func outranked(c *command.Ctx, t target) bool {
	return t.role == command.Owner || t.role >= c.Principal.Role && c.Principal.Role != command.Owner
}

func memberUpdate(c *command.Ctx, b json.RawMessage) (any, error) {
	if err := human(c); err != nil {
		return nil, err
	}
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	uid, _, err := m.id("user_id", true)
	if err != nil {
		return nil, err
	}
	t, err := loadTarget(c, uid)
	if err != nil {
		return nil, err
	}
	sets, args := []string{}, []any{uid}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, col+" = $"+strconv.Itoa(len(args)))
	}

	if role, ok, err := m.str("role", 10); err != nil {
		return nil, err
	} else if ok {
		if !slices.Contains([]string{"admin", "member", "guest"}, role) {
			return nil, command.ErrInvalid("role must be admin, member or guest")
		}
		if outranked(c, t) {
			return nil, command.ErrForbidden("cannot change this member's role")
		}
		if command.ParseRole(role) > c.Principal.Role {
			return nil, command.ErrForbidden("cannot grant a role above your own")
		}
		add("role", role)
	}
	if v, ok := m["finance_access"]; ok {
		var f bool
		if json.Unmarshal(v, &f) != nil {
			return nil, command.ErrInvalid("finance_access must be true or false")
		}
		if outranked(c, t) {
			return nil, command.ErrForbidden("cannot change this member's finance access")
		}
		add("finance_access", f)
	}
	if dep, ok, err := m.id("department_id", false); err != nil {
		return nil, err
	} else if ok {
		if dep != "" {
			var found bool
			if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from core.departments where id=$1::uuid)", dep).Scan(&found); err != nil {
				return nil, command.DB(err)
			}
			if !found {
				return nil, command.ErrInvalid("department not found")
			}
		}
		add("department_id", nilIfEmpty(dep))
	}
	if mgr, ok, err := m.id("manager_user_id", false); err != nil {
		return nil, err
	} else if ok {
		if mgr != "" {
			if mgr == uid {
				return nil, command.ErrInvalid("a person cannot be their own manager")
			}
			if err := activeMember(c, mgr); err != nil {
				return nil, err
			}
			var cycle bool // walk up from the proposed manager; reaching this person would close a loop
			if err := c.Tx.QueryRow(c.Context, `with recursive up(user_id, manager_user_id) as (
				select user_id, manager_user_id from core.memberships where user_id=$1::uuid
				union select m.user_id, m.manager_user_id from core.memberships m join up on m.user_id = up.manager_user_id)
				select exists(select 1 from up where user_id=$2::uuid)`, mgr, uid).Scan(&cycle); err != nil {
				return nil, command.DB(err)
			}
			if cycle {
				return nil, command.ErrInvalid("that would make a reporting loop")
			}
		}
		add("manager_user_id", nilIfEmpty(mgr))
	}
	for _, f := range []struct {
		k, col string
		max    int
	}{{"designation", "designation", 80}, {"phone", "phone", 25}} {
		v, ok, err := m.str(f.k, f.max)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if f.k == "phone" {
			if v = cleanPhone(v); v != "" && !phoneRe.MatchString(v) {
				return nil, command.ErrInvalid("phone is 7 to 15 digits, optionally starting with +")
			}
		}
		add(f.col, nilIfEmpty(v))
	}
	for _, k := range []string{"joined_on", "left_on"} {
		v, ok, err := m.date(k)
		if err != nil {
			return nil, err
		}
		if ok {
			add(k, nilIfEmpty(v))
		}
	}
	if len(sets) == 0 {
		return nil, command.ErrInvalid("nothing to update")
	}
	if _, err := c.Tx.Exec(c.Context, "update core.memberships set "+strings.Join(sets, ", ")+" where user_id = $1::uuid", args...); err != nil {
		return nil, command.DB(err)
	}
	var span bool
	if err := c.Tx.QueryRow(c.Context, "select coalesce(left_on < joined_on, false) from core.memberships where user_id=$1::uuid", uid).Scan(&span); err != nil {
		return nil, command.DB(err)
	}
	if span {
		return nil, command.ErrInvalid("left_on is before joined_on") // rolls back the whole command
	}
	if _, err := c.Emit("member.updated", nil, map[string]any{"user_id": uid, "fields": fieldNames(m)}); err != nil {
		return nil, err
	}
	return personRow(c, uid)
}

func fieldNames(m in) []string {
	out := []string{}
	for k := range m {
		if k != "user_id" {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func personRow(c *command.Ctx, uid string) (any, error) {
	all, err := peopleList(c, json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	for _, r := range all.([]map[string]any) {
		if r["user_id"] == uid {
			return r, nil
		}
	}
	return nil, command.ErrNotFound("member")
}

func memberDeactivate(c *command.Ctx, b json.RawMessage) (any, error) {
	if err := human(c); err != nil {
		return nil, err
	}
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	uid, _, err := m.id("user_id", true)
	if err != nil {
		return nil, err
	}
	t, err := loadTarget(c, uid)
	if err != nil {
		return nil, err
	}
	if c.Principal.UserID != nil && *c.Principal.UserID == uid {
		return nil, command.ErrInvalid("you cannot deactivate yourself")
	}
	if outranked(c, t) && !(t.role == command.Owner && c.Principal.Role == command.Owner) {
		return nil, command.ErrForbidden("cannot deactivate this member")
	}
	if !t.active {
		return personRow(c, uid)
	}
	if t.role == command.Owner {
		var others int
		if err := c.Tx.QueryRow(c.Context, "select count(*) from core.memberships where role='owner' and deactivated_at is null and user_id <> $1::uuid", uid).Scan(&others); err != nil {
			return nil, command.DB(err)
		}
		if others == 0 {
			return nil, command.ErrConflict("cannot deactivate the last owner")
		}
	}
	// Requests are refused from now on (the principal lookup checks deactivated_at); their keys and agents stop with them.
	for _, q := range []string{
		"update core.memberships set deactivated_at = now(), left_on = coalesce(left_on, (now() at time zone 'Asia/Kolkata')::date) where user_id = $1::uuid",
		"update core.memberships set manager_user_id = null where manager_user_id = $1::uuid",
		"update core.departments set head_user_id = null where head_user_id = $1::uuid",
		"update core.api_keys set revoked_at = now() where user_id = $1::uuid and revoked_at is null",
	} {
		if _, err := c.Tx.Exec(c.Context, q, uid); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Emit("member.deactivated", nil, map[string]any{"user_id": uid}); err != nil {
		return nil, err
	}
	return personRow(c, uid)
}

func memberReactivate(c *command.Ctx, b json.RawMessage) (any, error) {
	if err := human(c); err != nil {
		return nil, err
	}
	m, err := decode(b)
	if err != nil {
		return nil, err
	}
	uid, _, err := m.id("user_id", true)
	if err != nil {
		return nil, err
	}
	t, err := loadTarget(c, uid)
	if err != nil {
		return nil, err
	}
	if outranked(c, t) && !(t.role == command.Owner && c.Principal.Role == command.Owner) {
		return nil, command.ErrForbidden("cannot reactivate this member")
	}
	if t.active {
		return personRow(c, uid)
	}
	if _, err := c.Tx.Exec(c.Context, "update core.memberships set deactivated_at = null, left_on = null where user_id = $1::uuid", uid); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("member.reactivated", nil, map[string]any{"user_id": uid}); err != nil {
		return nil, err
	}
	return personRow(c, uid)
}
