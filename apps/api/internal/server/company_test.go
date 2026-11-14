package server_test

import (
	"strings"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules"
	"github.com/cnote/api/internal/modules/billing"
)

// Company profile, departments, people and finance access. Fixtures come from the Rust server; the deactivation
// checks also hit Rust itself, so run with RUST_API_URL pointing at a build that has the deactivated_at predicate.

func (b *bt) memberAs(name, role string) who {
	return b.env.addMember(b.owner, b.env.signup(name, false), role)
}

func (b *bt) people() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, r := range b.okList("people.list", map[string]any{}) {
		m := r.(map[string]any)
		out[m["user_id"].(string)] = m
	}
	return out
}

func TestCompanyProfileValidationAndPrinting(t *testing.T) {
	b := newBT(t, false)
	g := billing.CompleteGSTIN("29ABCDE1234F1Z")
	for name, in := range map[string]map[string]any{
		"bad gstin":      {"gstin": "29ABCDE1234F1Z0"},
		"pan format":     {"pan": "ABCDE1234"},
		"pan vs gstin":   {"pan": "ZZZZZ9999Z", "gstin": g},
		"ifsc":           {"bank_ifsc": "HDFC1001234"},
		"pin":            {"pincode": "060001"},
		"phone":          {"phone": "12345"},
		"upi":            {"upi_vpa": "no handle"},
		"account":        {"bank_account": "12ab"},
		"fy month":       {"fy_start_month": 13},
		"state vs gstin": {"gstin": g, "state_code": "27"},
	} {
		if r := b.call(b.owner, "company.update", in); r.status != 400 {
			t.Errorf("%s: want 400, got %d %s", name, r.status, r.body)
		}
	}
	got := b.ok("company.update", map[string]any{"legal_name": "Acme Works Pvt Ltd", "pan": "abcde1234f", "gstin": strings.ToLower(g),
		"address": "12 MG Road\nBengaluru", "pincode": "560001", "phone": "+91 98765-43210", "email": "accounts@example.test",
		"upi_vpa": "acme.works@okbank", "bank_name": "HDFC Bank", "bank_account": "123456789012", "bank_ifsc": "hdfc0001234", "fy_start_month": 4})
	eq(t, got["gstin"], g)
	eq(t, got["pan"], "ABCDE1234F")
	eq(t, got["state_code"], "29") // derived from the GSTIN
	eq(t, got["phone"], "+919876543210")
	eq(t, got["bank_details"], "HDFC Bank, A/c 123456789012, IFSC HDFC0001234") // what documents print
	// partial update leaves the rest; an empty string clears; invoicing still works off the same keys
	got = b.ok("company.update", map[string]any{"trade_name": "Acme", "phone": ""})
	eq(t, got["legal_name"], "Acme Works Pvt Ltd")
	eq(t, got["phone"], "")
	eq(t, got["trade_name"], "Acme")
	eq(t, b.ok("workspace.get", map[string]any{})["settings"].(map[string]any)["gstin"], g)
	b.issue(b.draft(b.contact, 0))

	// members read the profile but cannot change it; guests cannot read it; a key may read but not change it
	m := b.memberAs("Mina", "member")
	eq(t, b.okAs(m, "company.get", map[string]any{})["legal_name"], "Acme Works Pvt Ltd")
	b.errAs(m, "company.update", map[string]any{"trade_name": "x"}, 403, "forbidden")
	gu := b.memberAs("Gus", "guest")
	b.errAs(gu, "company.get", map[string]any{}, 403, "forbidden")
	key := b.env.apiKey(b.owner)
	b.errAs(key, "company.update", map[string]any{"trade_name": "x"}, 403, "forbidden")
	eq(t, b.okAs(key, "company.get", map[string]any{})["trade_name"], "Acme")
	// another workspace sees none of it
	o2 := b.env.signup("Other", true)
	eq(t, b.env.cmd(b.gosv.URL, o2, o2.ws, "company.get", map[string]any{}, nil).json()["legal_name"], "")
	eq(t, b.env.dbStrings(b.owner.ws, "select count(*)::text from core.events where kind='company.updated'"), []string{"2"})
}

func TestDepartmentsAndReportingLine(t *testing.T) {
	b := newBT(t, false)
	a := b.memberAs("Ana", "admin")
	m := b.memberAs("Mina", "member")
	n := b.memberAs("Nik", "member")
	d := b.okAs(a, "department.create", map[string]any{"name": " Site ops ", "head_user_id": m.userID})
	eq(t, d["name"], "Site ops")
	b.errAs(a, "department.create", map[string]any{"name": "Site ops"}, 409, "conflict")
	b.errAs(a, "department.create", map[string]any{"name": ""}, 400, "invalid")
	b.errAs(a, "department.create", map[string]any{"name": "X", "head_user_id": "00000000-0000-4000-8000-000000000000"}, 400, "invalid")
	b.errAs(m, "department.create", map[string]any{"name": "Nope"}, 403, "forbidden")
	b.errAs(b.env.apiKey(a), "department.create", map[string]any{"name": "Nope"}, 403, "forbidden")
	id := d["id"].(string)
	b.okAs(a, "department.update", map[string]any{"id": id, "name": "Operations"})
	b.errAs(a, "department.update", map[string]any{"id": "00000000-0000-4000-8000-000000000000", "name": "Z"}, 404, "not_found")

	// profile: department, manager, designation, phone, dates
	got := b.okAs(a, "member.update", map[string]any{"user_id": n.userID, "department_id": id, "manager_user_id": m.userID,
		"designation": "Site engineer", "phone": "98765 43210", "joined_on": "2026-04-01"})
	eq(t, got["department"], "Operations")
	eq(t, got["manager_name"], "Mina")
	eq(t, got["phone"], "9876543210")
	eq(t, got["joined_on"], "2026-04-01")
	b.errAs(a, "member.update", map[string]any{"user_id": n.userID, "phone": "abc"}, 400, "invalid")
	b.errAs(a, "member.update", map[string]any{"user_id": n.userID, "joined_on": "01-04-2026"}, 400, "invalid")
	b.errAs(a, "member.update", map[string]any{"user_id": n.userID, "left_on": "2026-01-01"}, 400, "invalid") // before joined_on
	eq(t, b.people()[n.userID]["left_on"], nil)                                                               // the refused update rolled back
	b.errAs(a, "member.update", map[string]any{"user_id": n.userID}, 400, "invalid")
	b.errAs(a, "member.update", map[string]any{"user_id": n.userID, "department_id": "00000000-0000-4000-8000-000000000000"}, 400, "invalid")
	// reporting loops and self-management are refused
	b.errAs(a, "member.update", map[string]any{"user_id": n.userID, "manager_user_id": n.userID}, 400, "invalid")
	b.errAs(a, "member.update", map[string]any{"user_id": m.userID, "manager_user_id": n.userID}, 400, "invalid")
	b.okAs(a, "member.update", map[string]any{"user_id": m.userID, "manager_user_id": a.userID})
	b.errAs(a, "member.update", map[string]any{"user_id": a.userID, "manager_user_id": n.userID}, 400, "invalid") // a -> n -> m -> a
	b.okAs(a, "member.update", map[string]any{"user_id": n.userID, "manager_user_id": nil, "designation": nil})
	eq(t, b.people()[n.userID]["manager_user_id"], nil)

	// the directory: members see it, guests do not
	eq(t, len(b.list(m, "people.list", map[string]any{})), 4)
	b.errAs(b.memberAs("Gus", "guest"), "people.list", map[string]any{}, 403, "forbidden")
	eq(t, b.list(m, "department.list", map[string]any{})[0].(map[string]any)["members"], float64(1))

	// deleting is destructive: people may; a key needs the group and then a human confirmation; members are unassigned
	key := b.env.apiKey(b.owner)
	b.errAs(key, "department.delete", map[string]any{"id": id}, 403, "forbidden")
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "destructive"}}})
	r := b.call(key, "department.delete", map[string]any{"id": id})
	eq(t, r.json()["confirmation_required"], true)
	eq(t, len(b.list(b.owner, "department.list", map[string]any{})), 1)
	b.errAs(m, "department.delete", map[string]any{"id": id}, 403, "forbidden")
	b.okAs(a, "department.delete", map[string]any{"id": id})
	eq(t, b.people()[n.userID]["department_id"], nil)
	eq(t, len(b.list(b.owner, "department.list", map[string]any{})), 0)
}

func TestMemberUpdateRolesAndAgents(t *testing.T) {
	b := newBT(t, false)
	a1, a2 := b.memberAs("Ana", "admin"), b.memberAs("Abe", "admin")
	m := b.memberAs("Mina", "member")
	eq(t, b.okAs(a1, "member.update", map[string]any{"user_id": m.userID, "role": "guest"})["role"], "guest")
	b.errAs(a1, "member.update", map[string]any{"user_id": m.userID, "role": "owner"}, 400, "invalid")
	b.errAs(a1, "member.update", map[string]any{"user_id": a2.userID, "role": "member"}, 403, "forbidden") // peer admin
	b.errAs(a1, "member.update", map[string]any{"user_id": b.owner.userID, "role": "admin"}, 403, "forbidden")
	b.errAs(b.owner, "member.update", map[string]any{"user_id": b.owner.userID, "role": "admin"}, 403, "forbidden") // last owner stays owner
	eq(t, b.ok("member.update", map[string]any{"user_id": a2.userID, "role": "member"})["role"], "member")
	b.errAs(m, "member.update", map[string]any{"user_id": m.userID, "role": "admin"}, 403, "forbidden")
	b.errAs(m, "member.deactivate", map[string]any{"user_id": a1.userID}, 403, "forbidden")
	b.errAs(a1, "member.update", map[string]any{"user_id": "00000000-0000-4000-8000-000000000000", "role": "member"}, 404, "not_found")
	b.errAs(a1, "member.update", map[string]any{"user_id": "nope", "role": "member"}, 400, "invalid")
	// people only: an owner's key and an agent are refused even when the admin group is switched on for them
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "admin"}}})
	key := b.env.apiKey(b.owner)
	for _, c := range []string{"member.update", "member.deactivate", "member.reactivate"} {
		b.errAs(key, c, map[string]any{"user_id": m.userID, "role": "member"}, 403, "forbidden")
	}
}

func TestDeactivateStopsAccessOnBothServers(t *testing.T) {
	b := newBT(t, false)
	a := b.memberAs("Ana", "admin")
	m := b.memberAs("Mina", "member")
	mk := b.env.apiKey(m)
	for _, base := range []string{b.gosv.URL, b.env.rust} {
		eq(t, b.env.cmd(base, m, m.ws, "note.list", map[string]any{"space_id": b.space}, nil).status, 200)
		eq(t, b.env.cmd(base, mk, m.ws, "note.list", map[string]any{"space_id": b.space}, nil).status, 200)
	}
	b.errAs(a, "member.deactivate", map[string]any{"user_id": a.userID}, 400, "invalid") // not yourself
	b.errAs(a, "member.deactivate", map[string]any{"user_id": b.owner.userID}, 403, "forbidden")
	b.errAs(b.owner, "member.deactivate", map[string]any{"user_id": b.owner.userID}, 400, "invalid")
	p := b.ok("member.deactivate", map[string]any{"user_id": m.userID})
	if p["deactivated_at"] == nil || p["left_on"] == nil {
		t.Fatalf("not marked: %v", p)
	}
	for _, base := range []string{b.gosv.URL, b.env.rust} {
		for _, w := range []who{m, mk} {
			if r := b.env.cmd(base, w, m.ws, "note.list", map[string]any{"space_id": b.space}, nil); r.status != 403 && r.status != 401 {
				t.Errorf("%s: deactivated member still gets %d %s", base, r.status, r.body)
			}
		}
	}
	// gone from pickers, still in the directory; the key stays dead after reactivation
	for _, r := range b.okList("member.list", map[string]any{}) {
		if r.(map[string]any)["user_id"] == m.userID {
			t.Fatal("deactivated member in member.list")
		}
	}
	eq(t, b.people()[m.userID]["deactivated_at"] != nil, true)
	eq(t, b.ok("member.deactivate", map[string]any{"user_id": m.userID})["user_id"], m.userID) // idempotent
	b.ok("member.reactivate", map[string]any{"user_id": m.userID})
	eq(t, b.env.cmd(b.gosv.URL, m, m.ws, "note.list", map[string]any{"space_id": b.space}, nil).status, 200)
	eq(t, b.env.cmd(b.gosv.URL, mk, m.ws, "note.list", map[string]any{"space_id": b.space}, nil).status, 401)
	eq(t, b.people()[m.userID]["left_on"], nil)

	// a second owner may be deactivated by an owner; the last one never
	o2 := b.memberAs("Olu", "member")
	b.adminSQL("update core.memberships set role='owner' where user_id='" + o2.userID + "'")
	b.ok("member.deactivate", map[string]any{"user_id": o2.userID})
	b.errAs(b.owner, "member.deactivate", map[string]any{"user_id": b.owner.userID}, 400, "invalid")
}

func (b *bt) adminSQL(sql string) { b.t.Helper(); adminSQL(b.env, b.owner.ws, sql) }

func TestFinanceAccessFlag(t *testing.T) {
	b := newBT(t, false)
	m := b.memberAs("Mina", "member")
	a := b.memberAs("Ana", "admin")
	inv := b.draft(b.contact, 0)
	eq(t, b.okAs(m, "invoice.get", map[string]any{"id": inv})["id"], inv) // on by default
	b.ok("member.update", map[string]any{"user_id": m.userID, "finance_access": false})
	b.ok("member.update", map[string]any{"user_id": a.userID, "finance_access": false}) // admins are not affected
	eq(t, b.people()[m.userID]["finance_access"], false)
	mk := b.env.apiKey(m)
	n := 0
	for _, c := range modules.Registry().List() {
		if !c.Finance {
			continue
		}
		n++
		if c.MinRole >= command.Admin {
			continue // refused by role before the flag matters
		}
		for _, w := range []who{m, mk} {
			r := b.call(w, c.Name, map[string]any{"id": inv})
			if r.status != 403 || !strings.Contains(string(r.body), "finance access") {
				t.Errorf("%s as %v: want finance 403, got %d %s", c.Name, w.bearer != "", r.status, r.body)
			}
		}
		if c.Group == command.GroupRead && c.MinRole <= command.Admin {
			if r := b.call(a, c.Name, map[string]any{"id": inv}); r.status == 403 && strings.Contains(string(r.body), "finance access") {
				t.Errorf("%s: admin blocked by the flag", c.Name)
			}
		}
	}
	if n < 30 {
		t.Fatalf("only %d finance commands marked", n)
	}
	// everything else still works: quotes, notes, the directory, the profile
	b.okAs(m, "company.get", map[string]any{})
	b.okAs(m, "people.list", map[string]any{})
	b.okAs(m, "note.list", map[string]any{"space_id": b.space})
	// the home dashboard drops the money tiles for them and keeps them for others
	noTile(t, b.home(m), "payments_pending", "bills_due")
	tile(t, b.home(b.owner), "payments_pending")
	tile(t, b.home(a), "payments_pending")
	// switching it back restores access
	b.ok("member.update", map[string]any{"user_id": m.userID, "finance_access": true})
	eq(t, b.okAs(m, "invoice.get", map[string]any{"id": inv})["id"], inv)
}
