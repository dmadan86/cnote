package server_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cnote/api/internal/modules/audit"
)

// WP7: audit viewer, denial events (SEC-014), data-subject export and erase. Go-only commands; fixtures via Rust.

func (b *bt) auditList(w who, in map[string]any) ([]map[string]any, string) {
	b.t.Helper()
	r := b.okAs(w, "audit.list", in)
	out := []map[string]any{}
	for _, e := range r["events"].([]any) {
		out = append(out, e.(map[string]any))
	}
	next, _ := r["next_cursor"].(string)
	return out, next
}

func (b *bt) agentKey(name string) who {
	return who{ws: b.owner.ws, bearer: b.must(b.owner, "agent.create", map[string]any{"name": name, "scopes": []string{"read", "write_notes", "manage_crm", "admin", "destructive"}})["api_key"].(string)}
}

func (b *bt) enableGroups(groups ...string) {
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": groups}})
}

func TestAuditListPermissionsFiltersAndCursor(t *testing.T) {
	b := newBT(t, false)
	admin := b.addMember(b.owner, b.signup("Ada", false), "admin")
	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	guest := b.addMember(b.owner, b.signup("Gus", false), "guest")
	inv := b.draft(b.contact, 0)
	b.issue(inv)
	b.ok("invoice.cancel", map[string]any{"id": inv, "reason": "wrong buyer"})

	for _, w := range []who{member, guest} {
		b.errAs(w, "audit.list", map[string]any{}, 403, "forbidden")
		b.errAs(w, "audit.export", map[string]any{}, 403, "forbidden")
		b.errAs(w, "audit.retention", map[string]any{}, 403, "forbidden")
		b.errAs(w, "data.request_list", map[string]any{}, 403, "forbidden")
	}
	all, _ := b.auditList(admin, map[string]any{"limit": 200})
	if len(all) < 4 {
		t.Fatalf("expected the invoice history, got %d events", len(all))
	}
	// who cancelled the invoice: filter by page and kind, with the actor name resolved
	c, _ := b.auditList(b.owner, map[string]any{"note_id": inv, "kind_prefix": "invoice.cancel"})
	eq(t, len(c), 1)
	eq(t, c[0]["actor_name"], "Tess")
	eq(t, c[0]["actor_id"], b.owner.userID)
	byActor, _ := b.auditList(b.owner, map[string]any{"actor_id": admin.userID})
	eq(t, len(byActor), 0)
	future, _ := b.auditList(b.owner, map[string]any{"from": day(1)})
	eq(t, len(future), 0)
	today, _ := b.auditList(b.owner, map[string]any{"from": day(-1), "to": day(1)})
	eq(t, len(today) > 0, true)
	// '%' in a filter is literal
	pct, _ := b.auditList(b.owner, map[string]any{"kind_prefix": "%"})
	eq(t, len(pct), 0)

	// cursor: pages are disjoint, ordered and cover everything
	var seen []float64
	cur := ""
	for i := 0; i < 100; i++ {
		in := map[string]any{"limit": 3}
		if cur != "" {
			in["cursor"] = cur
		}
		page, next := b.auditList(b.owner, in)
		for _, e := range page {
			seen = append(seen, e["id"].(float64))
		}
		if next == "" {
			break
		}
		cur = next
	}
	eq(t, len(seen), len(func() []map[string]any { a, _ := b.auditList(b.owner, map[string]any{"limit": 200}); return a }()))
	for i := 1; i < len(seen); i++ {
		if seen[i] >= seen[i-1] {
			t.Fatalf("cursor pages overlap or are out of order: %v", seen)
		}
	}

	for _, bad := range []map[string]any{{"note_id": "nope"}, {"from": "2026-13-45"}, {"cursor": "abc"}, {"limit": 0}, {"limit": 500}, {"actor_id": strings.Repeat("x", 99)}} {
		b.errIs("audit.list", bad, 400, "invalid")
	}

	// another workspace sees nothing of this one
	other := newBT(t, false)
	foreign, _ := other.auditList(other.owner, map[string]any{"note_id": inv})
	eq(t, len(foreign), 0)
	for _, e := range func() []map[string]any { a, _ := other.auditList(other.owner, map[string]any{"limit": 200}); return a }() {
		if e["note_id"] == inv {
			t.Fatal("cross-tenant audit leak")
		}
	}
}

func TestAuditExportCSVConfirmationAndEvent(t *testing.T) {
	b := newBT(t, false)
	b.ok("note.create", map[string]any{"space_id": b.space, "title": "=HYPERLINK(\"http://x\")"})
	b.enableGroups("read", "admin")
	agent := b.agentKey("auditor")
	r := b.okAs(agent, "audit.export", map[string]any{})
	eq(t, r["confirmation_required"], true) // bulk data leaves: a person confirms
	eq(t, len(b.okList("confirmation.list", map[string]any{})) >= 1, true)

	out := b.ok("audit.export", map[string]any{"kind_prefix": "note."})
	rows, err := csv.NewReader(strings.NewReader(out["csv"].(string))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	eq(t, rows[0][2], "kind")
	eq(t, len(rows)-1, int(n(out["rows"])))
	found := false
	for _, row := range rows[1:] {
		if strings.HasPrefix(row[7], "'=HYPERLINK") { // formula neutralised
			found = true
		}
		if strings.HasPrefix(row[7], "=") {
			t.Fatal("formula injection in CSV")
		}
	}
	eq(t, found, true)
	ev, _ := b.auditList(b.owner, map[string]any{"kind_prefix": "audit.exported"})
	eq(t, len(ev), 1)
	b.errIs("audit.export", map[string]any{"from": "bad"}, 400, "invalid")
}

func TestDenialsAreAuditedOncePerBurst(t *testing.T) {
	b := newBT(t, false)
	agent := b.agentKey("probe")
	key := b.apiKey(b.owner)
	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	for i := 0; i < 5; i++ {
		b.errAs(agent, "audit.list", map[string]any{}, 403, "forbidden") // admin tool group is off for agents
	}
	b.errAs(agent, "member.add", map[string]any{"email": "x@example.test", "role": "owner"}, 403, "forbidden")
	b.errAs(key, "workspace.update", map[string]any{"settings": map[string]any{}}, 403, "forbidden")
	b.errAs(member, "audit.list", map[string]any{}, 403, "forbidden") // a person hitting a 403 is not logged

	d, _ := b.auditList(b.owner, map[string]any{"kind_prefix": "command.denied", "limit": 50})
	got := map[string]int{}
	for _, e := range d {
		p := e["payload"].(map[string]any)
		got[p["command"].(string)+"|"+p["caller"].(string)]++
		if p["reason"] == "" {
			t.Fatal("denial without a reason")
		}
		if e["actor_type"] == "agent" {
			eq(t, e["actor_name"], "probe")
		}
	}
	eq(t, got["audit.list|Agent"], 1)
	eq(t, got["member.add|Agent"], 1)
	eq(t, got["workspace.update|ApiKey"], 1)
	eq(t, len(got), 3)
}

// contact with PII, deals, activities and a comment
func (b *bt) person(name, email string) string {
	c := b.must(b.owner, "contact.create", map[string]any{"space_id": b.space, "name": name, "kind": "person", "email": email, "phone": "9876543210", "state_code": "29"})
	return c["id"].(string)
}

func TestSubjectExportIsScopedToTheSubject(t *testing.T) {
	b := newBT(t, false)
	asha := b.person("Asha Verma", "asha@example.test")
	ravi := b.person("Ravi Khanna", "ravi@example.test")
	ia := b.draft(asha, 0)
	b.issue(ia)
	ir := b.draft(ravi, 0)
	b.issue(ir)
	b.ok("activity.log", map[string]any{"note_id": asha, "kind": "call", "summary": "Asha asked for a discount"})
	b.ok("activity.log", map[string]any{"note_id": ravi, "kind": "call", "summary": "Ravi secret remark"})

	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	admin := b.addMember(b.owner, b.signup("Ada", false), "admin")
	b.errAs(member, "data.export_subject", map[string]any{"contact_id": asha}, 403, "forbidden")
	b.errAs(b.apiKey(member), "data.export_subject", map[string]any{"contact_id": asha}, 403, "forbidden")

	b.enableGroups("read", "admin")
	r := b.okAs(b.agentKey("exporter"), "data.export_subject", map[string]any{"contact_id": asha})
	eq(t, r["confirmation_required"], true)
	eq(t, len(b.auditKind("data.exported")), 0) // nothing left the building yet

	out := b.okAs(admin, "data.export_subject", map[string]any{"contact_id": asha})
	body, _ := json.Marshal(out)
	s := string(body)
	for _, want := range []string{"asha@example.test", "Asha asked for a discount", ia} {
		if !strings.Contains(s, want) {
			t.Fatalf("export lacks %q", want)
		}
	}
	for _, leak := range []string{"ravi@example.test", "Ravi secret remark", ir, "Ravi Khanna"} {
		if strings.Contains(s, leak) {
			t.Fatalf("export leaks another person's data: %q", leak)
		}
	}
	eq(t, len(b.auditKind("data.exported")), 1)

	// the member export holds that member's records, never the password hash
	mex, _ := json.Marshal(b.ok("data.export_subject", map[string]any{"user_id": member.userID}))
	if !strings.Contains(string(mex), member.email) || strings.Contains(string(mex), "password") {
		t.Fatalf("member export wrong: %s", mex)
	}
	b.errIs("data.export_subject", map[string]any{}, 400, "invalid")
	b.errIs("data.export_subject", map[string]any{"contact_id": asha, "user_id": member.userID}, 400, "invalid")
	b.errIs("data.export_subject", map[string]any{"contact_id": "zzz"}, 400, "invalid")

	// another workspace's ids are not found, never an empty export
	other := newBT(t, false)
	other.errIs("data.export_subject", map[string]any{"contact_id": asha}, 404, "not_found")
	other.errIs("data.export_subject", map[string]any{"user_id": member.userID}, 404, "not_found")
	reqs := b.okList("data.request_list", map[string]any{})
	eq(t, len(reqs), 2)
	eq(t, len(other.okList("data.request_list", map[string]any{})), 0)
}

func (b *bt) auditKind(prefix string) []map[string]any {
	e, _ := b.auditList(b.owner, map[string]any{"kind_prefix": prefix})
	return e
}

// Every table that points at a contact or a note is either exported or has a written reason not to be.
func TestSubjectExportCoversEveryReference(t *testing.T) {
	e := newEnv(t)
	rows, err := e.pool.Query(context.Background(), `
select format('%s.%s.%s', ns.nspname, cl.relname, a.attname)
from pg_constraint c join pg_class cl on cl.oid = c.conrelid join pg_namespace ns on ns.oid = cl.relnamespace
join pg_attribute a on a.attrelid = c.conrelid and a.attnum = c.conkey[array_length(c.conkey, 1)]
where c.contype = 'f' and c.confrelid in ('crm.contacts'::regclass, 'core.notes'::regclass)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		if _, ok := audit.SubjectCatalog[k]; !ok {
			t.Errorf("%s references a contact or note: export it in data.export_subject or add a reason to audit.SubjectCatalog", k)
		}
	}
}

func TestEraseKeepsBooksRevisionsAndEvents(t *testing.T) {
	b := newBT(t, false)
	asha := b.person("Asha Verma", "asha@example.test") // has an issued invoice: partial erase
	drafty := b.person("Dev Draft", "dev@example.test") // only a draft: full erase
	ia := b.draft(asha, 0)
	b.issue(ia)
	b.draft(drafty, 0)
	b.ok("activity.log", map[string]any{"note_id": asha, "kind": "call", "summary": "Asha private remark"})
	b.ok("activity.log", map[string]any{"note_id": drafty, "kind": "call", "summary": "Dev private remark"})
	for _, id := range []string{asha, drafty} {
		if err := b.raw(`insert into core.note_revisions (workspace_id, note_id, number, title, content_md, reason) values ('` + b.owner.ws + `','` + id + `',1,'Old name','old body','edit')`); err != nil {
			t.Fatal(err)
		}
		if err := b.raw(`insert into ai.chunks (workspace_id, note_id, model, ordinal, content) values ('` + b.owner.ws + `','` + id + `','m',0,'chunk with Dev and Asha')`); err != nil {
			t.Fatal(err)
		}
	}
	revs := func() []string {
		return b.dbStrings(b.owner.ws, `select to_jsonb(r)::text from core.note_revisions r order by id`)
	}
	maxEv := b.dbStrings(b.owner.ws, `select coalesce(max(id),0)::text from core.events`)[0]
	events := func() []string { // the log as it was before the erase, row for row
		return b.dbStrings(b.owner.ws, `select to_jsonb(e)::text from core.events e where id <= $1::bigint order by id`, maxEv)
	}
	period := day(0)[:7]
	tb0, g0, rev0, ev0 := mustJSON(b.tb()), mustJSON(b.ok("report.gstr1", map[string]any{"period": period})), revs(), events()
	inv0 := b.dbStrings(b.owner.ws, `select to_jsonb(i)::text from billing.invoices i order by number nulls last, note_id`)

	// who may call it
	admin := b.addMember(b.owner, b.signup("Ada", false), "admin")
	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	args := map[string]any{"contact_id": asha, "reason": "customer request"}
	b.errAs(admin, "data.erase_subject", args, 403, "forbidden")
	b.errAs(member, "data.erase_subject", args, 403, "forbidden")
	b.enableGroups("read", "admin", "destructive")
	b.errAs(b.agentKey("eraser"), "data.erase_subject", args, 403, "forbidden")
	b.errAs(b.apiKey(b.owner), "data.erase_subject", args, 403, "forbidden") // human only
	b.errIs("data.erase_subject", map[string]any{"contact_id": asha}, 400, "invalid")
	b.errIs("data.erase_subject", map[string]any{"user_id": member.userID, "reason": "x"}, 400, "invalid")
	b.errIs("data.erase_subject", map[string]any{"contact_id": command0, "reason": "x"}, 404, "not_found")
	other := newBT(t, false)
	other.errIs("data.erase_subject", args, 404, "not_found")

	// unpaid invoice: refused unless the owner says so; nothing changes on refusal
	r := b.ok("data.erase_subject", args)
	eq(t, r["status"], "refused")
	eq(t, b.dbStrings(b.owner.ws, "select email from crm.contacts where note_id = $1::uuid", asha), []string{"asha@example.test"})
	args["confirm_unpaid"] = true
	r = b.ok("data.erase_subject", args)
	eq(t, r["status"], "done")
	eq(t, r["mode"], "partial")
	b.errIs("data.erase_subject", args, 409, "conflict")

	d := b.ok("data.erase_subject", map[string]any{"contact_id": drafty, "reason": "ex-lead asked"})
	eq(t, d["mode"], "full")

	// PII is gone from the contact, search, chunks (the indexer rebuilds them from the anonymised page), activities; statutory fields stay where an invoice needs them
	c := b.ok("contact.get", map[string]any{"id": drafty})
	eq(t, c["email"], nil)
	eq(t, c["phone"], nil)
	eq(t, strings.HasPrefix(c["name"].(string), "Erased contact"), true)
	eq(t, len(b.dbStrings(b.owner.ws, `select 1::text from core.notes where search @@ plainto_tsquery('simple','Dev') and id = $1::uuid`, drafty)), 0)
	eq(t, b.dbStrings(b.owner.ws, `select count(*)::text from ai.chunks where note_id in ($1::uuid,$2::uuid) and (content like '%chunk with%' or content like '%@example.test%' or content like '%Dev Draft%')`, asha, drafty), []string{"0"})
	eq(t, b.dbStrings(b.owner.ws, `select count(*)::text from crm.activities where summary like '%private remark%'`), []string{"0"})
	a := b.ok("contact.get", map[string]any{"id": asha})
	eq(t, a["email"], nil)
	eq(t, a["name"], "Asha Verma") // printed on the issued invoice, kept for the statutory period
	for _, h := range []string{"Asha", "Dev"} {
		for _, hit := range b.okList("search.notes", map[string]any{"q": h + " private"}) {
			if m := hit.(map[string]any); m["id"] == asha || m["id"] == drafty {
				if strings.Contains(mustJSON(m), "private remark") {
					t.Fatalf("erased text still searchable: %v", m)
				}
			}
		}
	}

	// books, revisions and the event log are untouched
	eq(t, mustJSON(b.tb()), tb0)
	eq(t, mustJSON(b.ok("report.gstr1", map[string]any{"period": period})), g0)
	eq(t, b.dbStrings(b.owner.ws, `select to_jsonb(i)::text from billing.invoices i order by number nulls last, note_id`), inv0)
	eq(t, revs(), rev0)
	eq(t, events(), ev0)
	if err := b.raw(`update core.note_revisions set content_md = 'x'`); err == nil {
		t.Fatal("revisions stayed mutable")
	}
	if err := b.raw(`delete from core.events`); err == nil {
		t.Fatal("events stayed deletable")
	}
	// the erase is on record, with ids only
	ev := b.auditKind("data.erased")
	eq(t, len(ev), 2)
	if p := strings.Join(b.dbStrings(b.owner.ws, `select payload::text from core.events where kind = 'data.erased'`), ""); strings.Contains(p, "Asha") || strings.Contains(p, "asha@") || strings.Contains(p, "Dev") {
		t.Fatal("erase event carries personal data")
	}
	reqs := b.okList("data.request_list", map[string]any{})
	eq(t, len(reqs), 3) // refused, partial, full
	// the request list is this workspace's only
	eq(t, len(other.okList("data.request_list", map[string]any{})), 0)
}

const command0 = "00000000-0000-0000-0000-000000000000"

func TestAuditRetentionReportOnly(t *testing.T) {
	b := newBT(t, false)
	r := b.ok("audit.retention", map[string]any{})
	eq(t, n(r["years"]), int64(8))
	eq(t, r["deletes"], false)
	r = b.ok("audit.retention", map[string]any{"years": 6})
	eq(t, n(r["years"]), int64(6))
	eq(t, n(b.ok("audit.retention", map[string]any{})["years"]), int64(6))
	b.errIs("audit.retention", map[string]any{"years": 0}, 400, "invalid")
	b.errIs("audit.retention", map[string]any{"years": 99}, 400, "invalid")
	eq(t, len(b.auditKind("audit.retention_set")), 1)
}

func TestEraseReplayAndReceiptsAreStatutory(t *testing.T) {
	b := newBT(t, false)
	c := b.person("Rita Rao", "rita@example.test")
	hdr := map[string]string{"Idempotency-Key": rnd()}
	if b.dbStrings(b.owner.ws, `select to_regclass('billing.receipts')::text`)[0] != "" {
		note := b.ok("note.create", map[string]any{"space_id": b.space, "title": "RV/1"})["id"].(string)
		if err := b.raw(`insert into billing.receipts (note_id, workspace_id, kind, number, contact_id, supply_type, supplier_gstin, supplier_state, amount_minor, taxable_minor, received_at)
			values ('` + note + `','` + b.owner.ws + `','receipt','RV/1','` + c + `','services','29ABCDE1234F1Z5','29',1000,1000,current_date)`); err != nil {
			t.Skipf("receipts table differs from this test's assumptions: %v", err)
		}
		ex, _ := json.Marshal(b.ok("data.export_subject", map[string]any{"contact_id": c}))
		if !strings.Contains(string(ex), "RV/1") {
			t.Fatal("export lacks the receipt")
		}
		eq(t, b.cmd(b.gosv.URL, b.owner, b.owner.ws, "data.erase_subject", map[string]any{"contact_id": c, "reason": "ask"}, hdr).json()["mode"], "partial")
	} else {
		eq(t, b.cmd(b.gosv.URL, b.owner, b.owner.ws, "data.erase_subject", map[string]any{"contact_id": c, "reason": "ask"}, hdr).json()["mode"], "full")
	}
	again := b.cmd(b.gosv.URL, b.owner, b.owner.ws, "data.erase_subject", map[string]any{"contact_id": c, "reason": "ask"}, hdr)
	eq(t, again.status, 200) // replayed, not "already erased"
	eq(t, len(b.auditKind("data.erased")), 1)
}
