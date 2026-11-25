package tables

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
)

func call(t *testing.T, c *command.Ctx, name string, in any) map[string]any {
	t.Helper()
	out, err := callErr(c, name, in)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func callErr(c *command.Ctx, name string, in any) (map[string]any, error) {
	b, _ := json.Marshal(in)
	res, err := handlers[name](c, b)
	if err != nil {
		return nil, err
	}
	rb, _ := json.Marshal(res)
	var m map[string]any
	_ = json.Unmarshal(rb, &m)
	return m, nil
}

func wantCode(t *testing.T, err error, code command.Code, msg string) {
	t.Helper()
	ce, ok := err.(*command.Error)
	if !ok || ce.Code != code || !strings.Contains(ce.Message, msg) {
		t.Errorf("got %v, want %s containing %q", err, code, msg)
	}
}

func openSpace(t *testing.T, c *command.Ctx) string {
	var id string
	if err := c.Tx.QueryRow(c.Context, `select id::text from core.spaces where key='OPEN'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func s(m map[string]any, k string) string { v, _ := m[k].(string); return v }

func TestStructureFlowAndRights(t *testing.T) {
	f := seed(t)
	var baseID, tableID, otherID, fieldID string
	f.as("outsider", command.Member, func(c *command.Ctx) { // a writer: member of an open space
		sp := openSpace(t, c)
		b := call(t, c, "base.create", map[string]any{"space_id": sp, "name": "  Shop "})
		baseID = s(b, "id")
		if s(b, "name") != "Shop" {
			t.Errorf("name not trimmed: %q", s(b, "name"))
		}
		tb := call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "Customers"})
		tableID = s(tb, "id")
		fs := tb["fields"].([]any)
		if len(fs) != 1 || s(fs[0].(map[string]any), "name") != "Name" || s(tb, "primary_field_id") != s(fs[0].(map[string]any), "id") || len(tb["views"].([]any)) != 1 {
			t.Errorf("default table shape wrong: %v", tb)
		}
		otherID = s(call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "Orders", "fields": []any{map[string]any{"name": "No", "type": "autonumber"}}}), "id")
		fld := call(t, c, "field.create", map[string]any{"table_id": otherID, "name": "Customer", "type": "link", "config": map[string]any{"table_id": tableID, "multiple": false}, "inverse": true})
		fieldID = s(fld, "id")
		cust := call(t, c, "table.get", map[string]any{"id": tableID})
		if len(cust["fields"].([]any)) != 2 {
			t.Errorf("inverse field missing: %v", cust["fields"])
		}
		// names are unique among live fields, case-insensitively
		_, err := callErr(c, "field.create", map[string]any{"table_id": tableID, "name": "name", "type": "text"})
		wantCode(t, err, command.Conflict, "already exists")
		// a writer renames fields, options and tables
		call(t, c, "field.update", map[string]any{"id": fieldID, "name": "Buyer"})
		call(t, c, "table.update", map[string]any{"id": otherID, "name": "Sales"})
		// but cannot delete or change types
		_, err = callErr(c, "field.delete", map[string]any{"id": fieldID})
		wantCode(t, err, command.Forbidden, "manager")
		_, err = callErr(c, "table.delete", map[string]any{"id": tableID})
		wantCode(t, err, command.Forbidden, "manager")
		_, err = callErr(c, "base.update", map[string]any{"id": baseID, "name": "x"})
		wantCode(t, err, command.Forbidden, "manager")
		// events carry ids only
		var n int
		_ = c.Tx.QueryRow(c.Context, `select count(*) from core.events where kind='field.created' and payload::text like '%Customer%'`).Scan(&n)
		if n != 0 {
			t.Error("event leaked a name")
		}
		_ = c.Tx.QueryRow(c.Context, `select count(*) from core.events where kind='field.created' and payload ? 'field_id'`).Scan(&n)
		if n == 0 {
			t.Error("no field.created event")
		}
	})
	f.as("viewer", command.Member, func(c *command.Ctx) {
		if err := c.Tx.QueryRow(c.Context, `insert into tables.tables(workspace_id, base_id, name) values ($1::uuid,$2::uuid,'T') returning id::text`, f.ws, f.openBase).Scan(&tableID); err != nil {
			t.Fatal(err)
		}
		_, err := callErr(c, "field.create", map[string]any{"table_id": tableID, "name": "X", "type": "text"})
		wantCode(t, err, command.Forbidden, "write")
		if call(t, c, "table.get", map[string]any{"id": tableID})["id"] != tableID {
			t.Error("viewer cannot read")
		}
	})
}

func TestManagerFlow(t *testing.T) {
	f := seed(t)
	f.as("admin", command.Admin, func(c *command.Ctx) {
		sp := openSpace(t, c)
		_, err := callErr(c, "base.create", map[string]any{"space_id": sp, "name": "B", "template": "nope"})
		wantCode(t, err, command.Invalid, "unknown template")
		baseID := s(call(t, c, "base.create", map[string]any{"space_id": sp, "name": "B"}), "id")
		a := s(call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "A"}), "id")
		bt := s(call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "B"}), "id")
		lnk := s(call(t, c, "field.create", map[string]any{"table_id": bt, "name": "To A", "type": "link", "config": map[string]any{"table_id": a}}), "id")
		_, err = callErr(c, "table.delete", map[string]any{"id": a})
		wantCode(t, err, command.Conflict, "link field")
		call(t, c, "field.delete", map[string]any{"id": lnk})
		call(t, c, "table.delete", map[string]any{"id": a})
		call(t, c, "table.restore", map[string]any{"id": a})
		call(t, c, "field.restore", map[string]any{"id": lnk})
		// the primary field stays
		got := call(t, c, "table.get", map[string]any{"id": a})
		_, err = callErr(c, "field.delete", map[string]any{"id": s(got, "primary_field_id")})
		wantCode(t, err, command.Invalid, "primary")
		// attachments are not in 1a; unknown types and bad config are refused
		_, err = callErr(c, "field.create", map[string]any{"table_id": a, "name": "F", "type": "attachment"})
		wantCode(t, err, command.Invalid, "not available")
		_, err = callErr(c, "field.create", map[string]any{"table_id": a, "name": "N", "type": "number", "config": map[string]any{"decimals": 9}})
		wantCode(t, err, command.Invalid, "decimals")
		_, err = callErr(c, "field.create", map[string]any{"table_id": a, "name": "L", "type": "link", "config": map[string]any{"table_id": newID()}})
		wantCode(t, err, command.Invalid, "same base")
		call(t, c, "base.delete", map[string]any{"id": baseID})
		if _, err := callErr(c, "base.get", map[string]any{"id": baseID}); err == nil {
			t.Error("deleted base readable")
		}
		call(t, c, "base.restore", map[string]any{"id": baseID})
		g := call(t, c, "base.get", map[string]any{"id": baseID})
		if g["role"] != "manage" || len(g["tables"].([]any)) != 2 {
			t.Errorf("base.get: %v", g)
		}
	})
	// a private space is invisible to a non-member
	f.as("outsider", command.Member, func(c *command.Ctx) {
		_, err := callErr(c, "base.get", map[string]any{"id": f.privBase})
		wantCode(t, err, command.NotFound, "")
		list, _ := handlers["base.list"](c, json.RawMessage(`{}`))
		for _, b := range list.([]baseOut) {
			if b.ID == f.privBase {
				t.Error("private base listed")
			}
		}
	})
}

func TestSelectOptionsAndConfig(t *testing.T) {
	f := seed(t)
	f.as("admin", command.Admin, func(c *command.Ctx) {
		baseID := s(call(t, c, "base.create", map[string]any{"space_id": openSpace(t, c), "name": "B"}), "id")
		tid := s(call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "T"}), "id")
		fld := call(t, c, "field.create", map[string]any{"table_id": tid, "name": "St", "type": "single_select",
			"config": map[string]any{"options": []any{map[string]any{"name": "New"}, map[string]any{"name": "Paid", "color": "green"}}}})
		cfg := fld["config"].(map[string]any)["options"].([]any)
		if s(cfg[0].(map[string]any), "id") != "o1" || s(cfg[1].(map[string]any), "id") != "o2" || s(cfg[0].(map[string]any), "color") != "gray" {
			t.Errorf("options: %v", cfg)
		}
		_, err := callErr(c, "field.update", map[string]any{"id": s(fld, "id"), "config": map[string]any{"options": []any{map[string]any{"name": "a"}, map[string]any{"name": "A"}}}})
		wantCode(t, err, command.Invalid, "two options")
		// a record uses o2; removing o2 strips it for a manager
		var rid string
		if err := c.Tx.QueryRow(c.Context, `insert into tables.records(workspace_id, table_id, seq, data) select workspace_id, id, 1, jsonb_build_object($2::text,'o2') from tables.tables where id=$1::uuid returning id::text`, tid, s(fld, "id")).Scan(&rid); err != nil {
			t.Fatal(err)
		}
		call(t, c, "field.update", map[string]any{"id": s(fld, "id"), "config": map[string]any{"options": []any{map[string]any{"id": "o1", "name": "Newer"}}}})
		var has bool
		_ = c.Tx.QueryRow(c.Context, `select data ? $2 from tables.records where id=$1::uuid`, rid, s(fld, "id")).Scan(&has)
		if has {
			t.Error("removed option still on the record")
		}
	})
	f.as("outsider", command.Member, func(c *command.Ctx) { // a writer may not remove an option that records use
		baseID := s(call(t, c, "base.create", map[string]any{"space_id": openSpace(t, c), "name": "B"}), "id")
		tid := s(call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "T"}), "id")
		fld := call(t, c, "field.create", map[string]any{"table_id": tid, "name": "St", "type": "single_select", "config": map[string]any{"options": []any{map[string]any{"name": "New"}}}})
		_, err := c.Tx.Exec(c.Context, `insert into tables.records(workspace_id, table_id, seq, data) select workspace_id, id, 1, jsonb_build_object($2::text,'o1') from tables.tables where id=$1::uuid`, tid, s(fld, "id"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = callErr(c, "field.update", map[string]any{"id": s(fld, "id"), "config": map[string]any{"options": []any{}}})
		wantCode(t, err, command.Forbidden, "manager")
	})
}

func TestTypeConversion(t *testing.T) {
	f := seed(t)
	f.as("admin", command.Admin, func(c *command.Ctx) {
		baseID := s(call(t, c, "base.create", map[string]any{"space_id": openSpace(t, c), "name": "B"}), "id")
		tid := s(call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "T"}), "id")
		mk := func(name, typ string, vals ...string) string {
			id := s(call(t, c, "field.create", map[string]any{"table_id": tid, "name": name, "type": typ}), "id")
			for i, v := range vals {
				if _, err := c.Tx.Exec(c.Context, `insert into tables.records(workspace_id, table_id, seq, data) select workspace_id, id, $3, jsonb_build_object($2::text, $4::jsonb) from tables.tables where id=$1::uuid
					on conflict (table_id, seq) do update set data = tables.records.data || excluded.data`, tid, id, i+1, v); err != nil {
					t.Fatal(err)
				}
			}
			return id
		}
		val := func(fid string, seq int) string {
			var v *string
			_ = c.Tx.QueryRow(c.Context, `select (data->$2)::text from tables.records where table_id=$1::uuid and seq=$3`, tid, fid, seq).Scan(&v)
			if v == nil {
				return "<absent>"
			}
			return *v
		}
		n := mk("Amt", "number", "1180.5", "50")
		// preview changes nothing
		p := call(t, c, "field.update", map[string]any{"id": n, "type": "currency"})
		if p["changed"].(float64) != 2 || p["cleared"].(float64) != 0 || len(p["samples"].([]any)) != 2 || val(n, 1) != "1180.5" {
			t.Errorf("preview: %v", p)
		}
		call(t, c, "field.update", map[string]any{"id": n, "type": "currency", "confirm": true})
		if val(n, 1) != "118050" || val(n, 2) != "5000" {
			t.Errorf("number->currency: %s %s", val(n, 1), val(n, 2))
		}
		call(t, c, "field.update", map[string]any{"id": n, "type": "text", "confirm": true})
		if val(n, 1) != `"₹1,180.50"` {
			t.Errorf("currency->text: %s", val(n, 1))
		}
		// text -> single_select builds options from the distinct values
		st := mk("Stage", "text", `"New"`, `"Paid"`, `"new"`)
		call(t, c, "field.update", map[string]any{"id": st, "type": "single_select", "confirm": true})
		got := call(t, c, "table.get", map[string]any{"id": tid})
		for _, fl := range got["fields"].([]any) {
			if m := fl.(map[string]any); s(m, "id") == st {
				if o := m["config"].(map[string]any)["options"].([]any); len(o) != 2 {
					t.Errorf("options: %v", o)
				}
			}
		}
		if val(st, 1) != `"o1"` || val(st, 3) != `"o1"` || val(st, 2) != `"o2"` {
			t.Errorf("select values: %s %s %s", val(st, 1), val(st, 2), val(st, 3))
		}
		call(t, c, "field.update", map[string]any{"id": st, "type": "multi_select", "confirm": true})
		if val(st, 2) != `["o2"]` {
			t.Errorf("single->multi: %s", val(st, 2))
		}
		// refusals: multi -> single, text -> number, and values that do not fit
		_, err := callErr(c, "field.update", map[string]any{"id": st, "type": "single_select", "confirm": true})
		wantCode(t, err, command.Invalid, "Create a new field and copy the values")
		em := mk("Mail", "text", `"a@b.in"`, `"nope"`)
		_, err = callErr(c, "field.update", map[string]any{"id": em, "type": "number", "confirm": true})
		wantCode(t, err, command.Invalid, "Create a new field")
		_, err = callErr(c, "field.update", map[string]any{"id": em, "type": "email", "confirm": true})
		wantCode(t, err, command.Conflict, "1 values cannot become email")
		if val(em, 2) != `"nope"` {
			t.Error("a refused conversion changed data")
		}
	})
	f.as("outsider", command.Member, func(c *command.Ctx) {
		baseID := s(call(t, c, "base.create", map[string]any{"space_id": openSpace(t, c), "name": "B"}), "id")
		tid := s(call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "T"}), "id")
		id := s(call(t, c, "field.create", map[string]any{"table_id": tid, "name": "N", "type": "number"}), "id")
		_, err := callErr(c, "field.update", map[string]any{"id": id, "type": "currency"})
		wantCode(t, err, command.Forbidden, "manager")
		_, err = callErr(c, "field.update", map[string]any{"id": id, "locked": true})
		wantCode(t, err, command.Forbidden, "manager")
	})
}

func TestLimits(t *testing.T) {
	f := seed(t)
	f.as("admin", command.Admin, func(c *command.Ctx) {
		sp := openSpace(t, c)
		call(t, c, "base.create", map[string]any{"space_id": sp, "name": "B"}) // the fixture already holds two
		_, err := callErr(c, "base.create", map[string]any{"space_id": sp, "name": "B4"})
		wantCode(t, err, command.Conflict, "3 bases on the Free plan")
	})
	f.as("admin", command.Admin, func(c *command.Ctx) {
		baseID := s(call(t, c, "base.create", map[string]any{"space_id": openSpace(t, c), "name": "B"}), "id")
		tid := s(call(t, c, "table.create", map[string]any{"base_id": baseID, "name": "T"}), "id")
		if err := checkRecordLimit(c.Context, c.Tx, baseID, tid, 1000); err != nil {
			t.Errorf("1000 on free: %v", err)
		}
		wantCode(t, checkRecordLimit(c.Context, c.Tx, baseID, tid, 1001), command.Conflict, "1,000 records on the Free plan")
		_, _ = c.Tx.Exec(c.Context, `update tables.bases set record_count=990 where id=$1::uuid`, baseID)
		wantCode(t, checkRecordLimit(c.Context, c.Tx, baseID, tid, 11), command.Conflict, "reached")
		_, _ = c.Tx.Exec(c.Context, `update tables.bases set record_count=0 where id=$1::uuid`, baseID)
		_, _ = c.Tx.Exec(c.Context, `update tables.tables set next_seq=100000 where id=$1::uuid`, tid) // table cap path: counts live rows, none here
		if err := checkRecordLimit(c.Context, c.Tx, baseID, tid, 5); err != nil {
			t.Errorf("empty table near next_seq cap: %v", err)
		}
	})
}

func TestFixtureTemplate(t *testing.T) {
	b, err := os.ReadFile("testdata/mini.json")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := parseTemplate(b)
	if err != nil {
		t.Fatal(err)
	}
	f := seed(t)
	f.as("admin", command.Admin, func(c *command.Ctx) {
		sp := openSpace(t, c)
		var id string
		_ = c.Tx.QueryRow(c.Context, `insert into tables.bases(workspace_id, space_id, name) values ($1::uuid,$2::uuid,'M') returning id::text`, f.ws, sp).Scan(&id)
		if err := instantiate(c, Base{ID: id, WorkspaceID: f.ws, SpaceID: sp}, tpl); err != nil {
			t.Fatal(err)
		}
		g := call(t, c, "base.get", map[string]any{"id": id})
		if g["usage"].(map[string]any)["records"].(float64) != 4 {
			t.Errorf("usage: %v", g["usage"])
		}
		var links int
		_ = c.Tx.QueryRow(c.Context, `select count(*) from tables.links where base_id=$1::uuid`, id).Scan(&links)
		if links != 2 {
			t.Errorf("links: %d", links)
		}
		for _, tb := range g["tables"].([]any) {
			m := tb.(map[string]any)
			if s(m, "name") == "Orders" {
				if len(m["fields"].([]any)) != 4 || len(m["views"].([]any)) != 2 { // 4 fields in order; kanban + default grid
					t.Errorf("orders: %v", m)
				}
				v := m["views"].([]any)[1].(map[string]any)["config"].(map[string]any)
				if st := v["kanban"].(map[string]any)["stack"].(string); !uuidRe.MatchString(st) {
					t.Errorf("stack not mapped to an id: %v", v)
				}
			}
			if s(m, "name") == "Customers" {
				if len(m["fields"].([]any)) != 3 { // + the inverse field
					t.Errorf("customers fields: %v", m["fields"])
				}
				if g := m["views"].([]any)[0].(map[string]any)["config"].(map[string]any)["group"].(map[string]any); !uuidRe.MatchString(s(g, "field")) {
					t.Errorf("group not mapped: %v", g)
				}
			}
		}
	})
}

// Every shipped template builds through base.create on the free plan's tightest limits.
func TestAllEmbeddedTemplatesInstantiate(t *testing.T) {
	all, err := allTemplates()
	if err != nil || len(all) == 0 {
		t.Fatalf("templates: %v %d", err, len(all))
	}
	f := seed(t)
	for _, tp := range all {
		f.as("admin", command.Admin, func(c *command.Ctx) {
			b := call(t, c, "base.create", map[string]any{"space_id": openSpace(t, c), "name": tp.Name, "template": tp.Key})
			if int(b["table_count"].(float64)) != len(tp.Tables) || b["template"] != tp.Key {
				t.Errorf("%s: %v", tp.Key, b)
			}
			g := call(t, c, "base.get", map[string]any{"id": s(b, "id")})
			if g["usage"].(map[string]any)["records"].(float64) == 0 {
				t.Errorf("%s: no records", tp.Key)
			}
		})
	}
	f.as("admin", command.Admin, func(c *command.Ctx) {
		out, err := handlers["table_template.list"](c, json.RawMessage(`{}`))
		if err != nil || len(out.([]map[string]any)) != len(all) {
			t.Errorf("list: %v %v", out, err)
		}
	})
}
