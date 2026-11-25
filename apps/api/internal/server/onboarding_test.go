package server_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
)

type setupRow struct {
	Key  string `json:"key"`
	Done bool   `json:"done"`
	Href string `json:"href"`
}

func (b *bt) setup(w who) []setupRow {
	b.t.Helper()
	r := b.call(w, "setup.status", map[string]any{})
	if r.status != 200 {
		b.t.Fatalf("setup.status: %d %s", r.status, r.body)
	}
	var rows []setupRow
	if err := json.Unmarshal(r.body, &rows); err != nil {
		b.t.Fatal(err)
	}
	return rows
}

func rowMap(rows []setupRow) map[string]bool {
	m := map[string]bool{}
	for _, r := range rows {
		m[r.Key] = r.Done
	}
	return m
}

func TestOnboardingRoles(t *testing.T) {
	b := newBT(t, false)
	admin := b.addMember(b.owner, b.signup("A", false), "admin")
	member := b.addMember(b.owner, b.signup("M", false), "member")
	guest := b.addMember(b.owner, b.signup("G", false), "guest")
	for _, cmd := range []string{"setup.status", "onboarding.complete"} {
		for name, w := range map[string]who{"owner": b.owner, "admin": admin} {
			if r := b.call(w, cmd, map[string]any{}); r.status != 200 {
				t.Fatalf("%s as %s: %d %s", cmd, name, r.status, r.body)
			}
		}
		for name, w := range map[string]who{"member": member, "guest": guest} {
			if r := b.call(w, cmd, map[string]any{}); r.status != 403 {
				t.Fatalf("%s as %s: want 403, got %d %s", cmd, name, r.status, r.body)
			}
		}
		if r := b.call(who{}, cmd, map[string]any{}); r.status != 401 {
			t.Fatalf("%s without auth: %d", cmd, r.status)
		}
	}
	// the setup rows are never reachable through their internal checks
	if r := b.call(b.owner, "setup.customer_exists", map[string]any{}); r.status != 404 {
		t.Fatalf("internal check over HTTP: %d", r.status)
	}
}

func TestSetupStatusRowsTickFromData(t *testing.T) {
	b := newBT(t, false)
	rows := b.setup(b.owner)
	keys := []string{}
	for _, r := range rows {
		keys = append(keys, r.Key)
		if r.Href == "" {
			t.Fatalf("row %s has no href", r.Key)
		}
	}
	eq(t, keys, []string{"company", "customer", "invoice", "website", "table", "import"})
	m := rowMap(rows)
	eq(t, m["customer"], true) // newBT makes one customer
	for _, k := range []string{"company", "invoice", "website", "table", "import"} {
		eq(t, m[k], false)
	}

	b.ok("company.update", map[string]any{"legal_name": "Sharma Electricals"})
	inv := b.draft(b.contact, 0)
	eq(t, rowMap(b.setup(b.owner))["invoice"], false) // a draft does not count
	b.issue(inv)
	b.ok("base.create", map[string]any{"space_id": b.space, "name": "Orders"})
	if err := b.raw("insert into importers.jobs (workspace_id, source, kind, status) values ('" + b.owner.ws + "', 'data_import', 'contacts', 'done')"); err != nil {
		t.Fatal(err)
	}
	m = rowMap(b.setup(b.owner))
	for _, k := range []string{"company", "customer", "invoice", "table", "import"} {
		eq(t, m[k], true)
	}
	eq(t, m["website"], false)

	// rows for modules the workspace lacks are left out
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"hidden_modules": []string{"billing", "site", "tables"}}})
	keys = nil
	for _, r := range b.setup(b.owner) {
		keys = append(keys, r.Key)
	}
	eq(t, keys, []string{"company", "customer", "import"})
}

func TestSetupStatusTenantIsolation(t *testing.T) {
	a := newBT(t, false)
	o := a.env.tenant(false)
	a.issue(a.draft(a.contact, 0))
	a.ok("base.create", map[string]any{"space_id": a.space, "name": "Mine"})
	eq(t, rowMap(o.setup(o.owner))["invoice"], false)
	eq(t, rowMap(o.setup(o.owner))["table"], false)
	eq(t, rowMap(a.setup(a.owner))["invoice"], true)
	// a path to a foreign workspace is refused
	if r := o.env.cmd(o.gosv.URL, o.owner, a.owner.ws, "setup.status", map[string]any{}, nil); r.status == 200 {
		t.Fatalf("foreign workspace: %s", r.body)
	}
	// completing one workspace leaves the other alone
	a.ok("onboarding.complete", map[string]any{})
	eq(t, o.ok("workspace.get", map[string]any{})["settings"].(map[string]any)["onboarding"], nil)
}

func TestOnboardingCompleteIsMonotonic(t *testing.T) {
	b := newBT(t, false)
	first := b.ok("onboarding.complete", map[string]any{})["done_at"].(string)
	if first == "" {
		t.Fatal("no done_at")
	}
	eq(t, b.ok("onboarding.complete", map[string]any{})["done_at"], first)
	if r := b.call(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"onboarding": map[string]any{"done_at": "2000-01-01T00:00:00Z"}}}); r.status != 400 {
		t.Fatalf("workspace.update must not write onboarding: %d %s", r.status, r.body)
	}

	// two calls at once on a fresh workspace both succeed and agree on one timestamp
	o := b.env.tenant(false)
	got := make([]string, 8)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := o.call(o.owner, "onboarding.complete", map[string]any{})
			if r.status != 200 {
				t.Errorf("concurrent call: %d %s", r.status, r.body)
				return
			}
			got[i], _ = r.json()["done_at"].(string)
		}()
	}
	wg.Wait()
	for _, g := range got {
		if g == "" || g != got[0] {
			t.Fatalf("timestamps differ: %v", got)
		}
	}
	st := o.ok("workspace.get", map[string]any{})["settings"].(map[string]any)["onboarding"].(map[string]any)
	eq(t, st["done_at"], got[0])
}

// An old preset `modules` list hides nothing; only hidden_modules does (setup rows and Home tiles).
func TestLegacyModulesListHidesNothing(t *testing.T) {
	b := newBT(t, false)
	summary := func() map[string]any {
		r := b.call(b.owner, "home.summary", map[string]any{})
		if r.status != 200 {
			t.Fatalf("home.summary: %d %s", r.status, r.body)
		}
		var out struct {
			Tiles map[string]any `json:"tiles"`
		}
		if err := json.Unmarshal(r.body, &out); err != nil {
			t.Fatal(err)
		}
		return out.Tiles
	}
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"modules": []string{"docs", "crm"}}})
	keys := []string{}
	for _, r := range b.setup(b.owner) {
		keys = append(keys, r.Key)
	}
	eq(t, keys, []string{"company", "customer", "invoice", "website", "table", "import"})
	if _, ok := summary()["payments_pending"]; !ok {
		t.Fatal("billing tile hidden by a legacy modules list")
	}
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"hidden_modules": []string{"billing"}}})
	if _, ok := summary()["payments_pending"]; ok {
		t.Fatal("billing tile still shown with billing in hidden_modules")
	}
}

// migrations/1330: workspaces that uninstalled a Marketplace app before hidden_modules existed keep it uninstalled,
// and nothing else (docs, projects, crm, billing, tables, website) is hidden by a leftover `modules` list.
func TestHiddenModulesMigration(t *testing.T) {
	sqlText, err := os.ReadFile("../../../../migrations/1330_hidden_modules.sql")
	if err != nil {
		t.Fatal(err)
	}
	b := newBT(t, false)
	mk := func(settings map[string]any) *bt {
		o := b.env.tenant(false)
		o.ok("workspace.update", map[string]any{"settings": settings})
		return o
	}
	uninstalled := mk(map[string]any{"modules": []string{"docs", "projects", "crm", "billing"}}) // whiteboard uninstalled
	preset := mk(map[string]any{"modules": []string{"docs", "crm", "billing"}})                  // preset, projects not listed
	reinstalled := mk(map[string]any{"modules": []string{"docs", "crm", "whiteboard"}})          // whiteboard installed
	none := mk(map[string]any{})
	already := mk(map[string]any{"modules": []string{"docs"}, "hidden_modules": []string{"crm"}})
	if _, err := adminPool(t).Exec(context.Background(), string(sqlText)); err != nil {
		t.Fatal(err)
	}
	hidden := func(o *bt) any {
		return o.ok("workspace.get", map[string]any{})["settings"].(map[string]any)["hidden_modules"]
	}
	eq(t, hidden(uninstalled), []any{"whiteboard"})
	eq(t, hidden(preset), []any{"whiteboard"})
	eq(t, hidden(reinstalled), nil)
	eq(t, hidden(none), nil)
	eq(t, hidden(already), []any{"crm"})
}

// access.me tells the web whether money commands are open to the caller, by the check invoice.create_draft uses.
func TestAccessMeFinance(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("M", false), "member")
	finance := func(w who) bool {
		r := b.call(w, "access.me", map[string]any{})
		if r.status != 200 {
			t.Fatalf("access.me: %d %s", r.status, r.body)
		}
		return r.json()["finance"] == true
	}
	if !finance(b.owner) {
		t.Fatal("owner has finance access")
	}
	b.ok("member.update", map[string]any{"user_id": member.userID, "finance_access": false})
	if finance(member) {
		t.Fatal("member without finance access reported as having it")
	}
	if r := b.call(member, "invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "lines": []map[string]any{{"description": "x", "unit_price_minor": 1}}}); r.status != 403 {
		t.Fatalf("invoice.create_draft must refuse them too: %d %s", r.status, r.body)
	}
	b.ok("member.update", map[string]any{"user_id": member.userID, "finance_access": true})
	if !finance(member) {
		t.Fatal("member with finance access reported as having none")
	}
}
