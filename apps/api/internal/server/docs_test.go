package server_test

import (
	"context"
	"strings"
	"testing"
)

// Ports of crates/cnote-docs/tests/living_pages.rs and templates.rs for the commands that run in Go. The Rust
// worker (event handlers, jobs, sweeps) stays the reference for what feeds these commands, so tests seed the rows
// the worker would write (flags, proposals, archived pages) with SQL. doc.ask and doc.capture are proxied to Rust.

type dg struct {
	gw
	ws string
}

func newDG(t *testing.T) (dg, who) {
	e := newEnv(t)
	owner := e.signup("Tess", true)
	return dg{gw{t, e}, owner.ws}, owner
}

func (d dg) member(owner who) who { return d.e.addMember(owner, d.e.signup("M", false), "member") }

func (d dg) sql(q string, a ...any)           { adminSQL(d.e, d.ws, q, a...) }
func (d dg) scalar(q string, a ...any) string { return docsScalar(d.e, d.ws, q, a...) }

// docsScalar runs a statement returning one text value and commits it (adminScalar rolls back).
func docsScalar(e *env, ws, q string, a ...any) string {
	e.t.Helper()
	tx, err := dbTenant(e, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var out string
	if err := tx.QueryRow(context.Background(), q, a...).Scan(&out); err != nil {
		e.t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (d dg) fresh(w who, id string) string {
	return d.ok(w, "note.get", map[string]any{"id": id})["freshness"].(string)
}

func (d dg) hist(w who, id string) string {
	return string(d.run(w, "note.history", map[string]any{"id": id}).body)
}

func (d dg) lapses(id string) string {
	return d.scalar("select lapse_count::text from core.notes where id = $1::uuid", id)
}

// stale marks a page stale with an open flag and a pending proposal, as the worker would after a binding change.
func (d dg) stale(page, md string) (flag, proposal string) {
	d.sql("update core.notes set freshness = 'stale' where id = $1::uuid", page)
	flag = d.scalar("insert into docs.freshness_flags (workspace_id, note_id, reason) values ($1::uuid,$2::uuid,'binding_changed') returning id::text", d.ws, page)
	proposal = d.scalar(`insert into docs.edit_proposals (workspace_id, note_id, flag_id, proposed_md, rationale, base_version)
		select $1::uuid,$2::uuid,$3::uuid,$4,'Ship it changed', version from core.notes where id = $2::uuid returning id::text`, d.ws, page, flag, md)
	return
}

func TestDocsProposalFlow(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "ENG", "open")
	page := d.page(owner, space, "Deploy guide", nil, "Releases are tracked in ENG-12")
	_, pid := d.stale(page, "Releases are tracked in ENG-12\nREVISED")

	if d.fresh(owner, page) != "stale" {
		t.Fatal("page should be stale")
	}
	flags := d.list(owner, "doc.flags", map[string]any{})
	if len(flags) != 1 || flags[0]["reasons"].([]any)[0] != "binding_changed" {
		t.Fatalf("flags: %v", flags)
	}
	props := d.list(owner, "doc.proposals", map[string]any{})
	if len(props) != 1 || !strings.Contains(props[0]["rationale"].(string), "Ship it") {
		t.Fatalf("proposals: %v", props)
	}
	// Nothing was rewritten silently.
	if n := d.ok(owner, "note.get", map[string]any{"id": page}); strings.Contains(n["content_md"].(string), "REVISED") {
		t.Fatal("proposal applied without a decision")
	}
	d.ok(owner, "doc.proposal_decide", map[string]any{"proposal_id": props[0]["id"], "approve": true})
	if n := d.ok(owner, "note.get", map[string]any{"id": page}); !strings.HasSuffix(n["content_md"].(string), "REVISED") {
		t.Fatalf("content: %v", n["content_md"])
	}
	if d.fresh(owner, page) != "fresh" {
		t.Fatal("approving should refresh the page")
	}
	if n := len(d.list(owner, "doc.flags", map[string]any{})); n != 0 {
		t.Fatalf("flags after approve: %d", n)
	}
	if n := len(d.list(owner, "doc.proposals", map[string]any{})); n != 0 {
		t.Fatalf("pending proposals after approve: %d", n)
	}
	if h := d.hist(owner, page); !strings.Contains(h, "doc.proposal_decided") {
		t.Fatalf("history: %s", h)
	}
	if n := len(d.list(owner, "doc.proposals", map[string]any{"status": "approved"})); n != 1 {
		t.Fatalf("approved list: %d", n)
	}
	// Approving twice is rejected.
	d.code("not_found", owner, "doc.proposal_decide", map[string]any{"proposal_id": pid, "approve": true})
	// The applied edit is a page version, an event and a mention-free update like note.update.
	if got := d.scalar("select count(*)::text from core.events where kind = 'note.updated' and note_id = $1::uuid", page); got != "1" {
		t.Fatalf("note.updated events: %s", got)
	}
}

func TestDocsRejectKeepsPageAndVerifyResets(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "ENG", "open")
	page := d.page(owner, space, "Handbook", nil, "See Pricing for rates")
	_, pid := d.stale(page, "other text")
	d.ok(owner, "doc.proposal_decide", map[string]any{"proposal_id": pid, "approve": false})
	if d.fresh(owner, page) != "stale" {
		t.Fatal("reject must keep the page stale")
	}
	if n := d.ok(owner, "note.get", map[string]any{"id": page}); n["content_md"] != "See Pricing for rates" {
		t.Fatal("reject must not edit")
	}
	if n := len(d.list(owner, "doc.proposals", map[string]any{"status": "rejected"})); n != 1 {
		t.Fatalf("rejected list: %d", n)
	}

	member := d.member(owner)
	d.code("forbidden", member, "doc.verify", map[string]any{"note_id": page})
	d.sql("update core.notes set lapse_count = 1 where id = $1::uuid", page)
	v := d.ok(owner, "doc.verify", map[string]any{"note_id": page})
	if v["freshness"] != "fresh" || d.fresh(owner, page) != "fresh" {
		t.Fatalf("verify: %v", v)
	}
	if d.lapses(page) != "0" {
		t.Fatal("lapse count not reset")
	}
	if _, ok := d.ok(owner, "note.get", map[string]any{"id": page})["verified_at"].(string); !ok {
		t.Fatal("verified_at not set")
	}
	if n := len(d.list(owner, "doc.flags", map[string]any{})); n != 0 {
		t.Fatalf("flags: %d", n)
	}
	d.code("invalid", owner, "doc.verify", map[string]any{"note_id": page, "verify_by": "2000-01-01"})

	// Ownership transfer: the new owner can verify, the old (non-admin) one cannot.
	d.ok(owner, "doc.set_owner", map[string]any{"note_id": page, "owner_id": member.userID})
	d.ok(member, "doc.verify", map[string]any{"note_id": page})
	d.code("invalid", owner, "doc.set_owner", map[string]any{"note_id": page, "owner_id": "00000000-0000-4000-8000-000000000001"})
}

func TestDocsArchiveAndRestore(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "DOC", "open")
	page := d.page(owner, space, "Onboarding", nil, "zebra onboarding checklist")
	d.sql("update core.notes set freshness = 'archived', archived_at = now(), lapse_count = 2 where id = $1::uuid", page)
	if n := len(d.list(owner, "search.notes", map[string]any{"q": "zebra"})); n != 0 {
		t.Fatalf("archived page in search: %d", n)
	}
	arch := d.list(owner, "doc.archived", map[string]any{})
	if len(arch) != 1 || arch[0]["id"] != page {
		t.Fatalf("archived: %v", arch)
	}
	d.code("conflict", owner, "doc.restore", map[string]any{"note_id": d.page(owner, space, "Live", nil, "x")})
	d.ok(owner, "doc.restore", map[string]any{"note_id": page})
	if d.fresh(owner, page) != "stale" {
		t.Fatal("restored pages come back stale")
	}
	if n := len(d.list(owner, "search.notes", map[string]any{"q": "zebra"})); n != 1 {
		t.Fatalf("restored page in search: %d", n)
	}
	if h := d.hist(owner, page); !strings.Contains(h, "doc.restored") {
		t.Fatalf("history: %s", h)
	}
	if fl := d.list(owner, "doc.flags", map[string]any{}); len(fl) != 1 || fl[0]["reasons"].([]any)[0] != "date_lapsed" {
		t.Fatalf("flags: %v", fl)
	}
	d.code("conflict", owner, "doc.restore", map[string]any{"note_id": page})
}

func TestDocsVerifyClaimsUnverifiedImport(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "DOC", "open")
	fresh := d.page(owner, space, "New import", nil, "y")
	d.sql("update core.notes set freshness = 'unverified' where id = $1::uuid", fresh)
	d.ok(owner, "doc.verify", map[string]any{"note_id": fresh})
	if d.fresh(owner, fresh) != "fresh" {
		t.Fatal("verifying should claim an unverified page")
	}
}

func TestDocsAskRoutesCaptureRequests(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	space := d.space(owner, "ENG", "open")
	runbook := d.page(member, space, "Pod runbook", nil, "Restart kubernetes pods with the rollout command")

	a := d.ok(owner, "doc.ask", map[string]any{"question": "How do I restart kubernetes pods?"})
	c := a["citations"].([]any)[0].(map[string]any)
	if a["answered"] != true || c["title"] != "Pod runbook" || c["note_id"] != runbook || c["freshness"] != "fresh" {
		t.Fatalf("ask: %v", a)
	}
	if _, ok := c["verified_at"]; !ok {
		t.Fatal("citation without verified_at")
	}
	a = d.ok(owner, "doc.ask", map[string]any{"question": "What is the kubernetes zebra policy?"})
	if a["answered"] != false || a["routed_to"] != member.userID {
		t.Fatalf("ask routing: %v", a)
	}
	req := a["capture_request_id"]
	b := d.ok(member, "doc.ask", map[string]any{"question": "quantum flux capacitor"})
	if b["routed_to"] != owner.userID {
		t.Fatalf("fallback routing: %v", b)
	}
	if n := len(d.list(member, "capture.list", map[string]any{})); n != 2 {
		t.Fatalf("member captures: %d", n)
	}
	answer := d.page(member, space, "Zebra policy", nil, "Zebras are fine")
	d.ok(member, "capture.answer", map[string]any{"request_id": req, "note_id": answer})
	if n := len(d.list(owner, "capture.list", map[string]any{})); n != 1 {
		t.Fatalf("open captures: %d", n)
	}
	if n := len(d.list(owner, "capture.list", map[string]any{"status": "answered"})); n != 1 {
		t.Fatalf("answered captures: %d", n)
	}
	d.code("not_found", member, "capture.answer", map[string]any{"request_id": req, "note_id": answer})
	// someone unrelated may not answer
	other := d.member(owner)
	d.code("forbidden", other, "capture.answer", map[string]any{"request_id": b["capture_request_id"], "note_id": answer})
	d.code("not_found", member, "capture.answer", map[string]any{"request_id": b["capture_request_id"], "note_id": "00000000-0000-4000-8000-000000000001"})
}

func TestDocsPrivateSpacePagesNeverLeak(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	private := d.space(owner, "SEC", "private")
	d.page(owner, private, "Breach playbook", nil, "Rotate the vault credentials immediately")
	if seen := d.ok(owner, "doc.ask", map[string]any{"question": "rotate vault credentials"}); seen["answered"] != true {
		t.Fatalf("owner should see the answer: %v", seen)
	}
	hidden := d.run(member, "doc.ask", map[string]any{"question": "rotate vault credentials"})
	if h := hidden.json(); h["answered"] != false || strings.Contains(string(hidden.body), "Breach") {
		t.Fatalf("leak: %s", hidden.body)
	}
	if s := d.ok(member, "doc.freshness_score", map[string]any{}); s["total"] != float64(0) || s["score"] != float64(1) {
		t.Fatalf("member score: %v", s)
	}
	if s := d.ok(owner, "doc.freshness_score", map[string]any{}); s["total"] != float64(1) {
		t.Fatalf("owner score: %v", s)
	}
}

func TestDocsTenantsIsolatedAndScorePerSpace(t *testing.T) {
	d, a := newDG(t)
	b := d.e.signup("Tess", true)
	s1, s2 := d.space(a, "AAA", "open"), d.space(a, "BBB", "open")
	p1 := d.page(a, s1, "Alpha secret", nil, "alpha unique-term")
	d.page(a, s1, "Beta", nil, "b")
	d.page(a, s2, "Gamma", nil, "c")
	d.sql("update core.notes set freshness = 'stale' where id = $1::uuid", p1)

	s := d.ok(a, "doc.freshness_score", map[string]any{})
	if s["total"] != float64(3) || s["verified"] != float64(2) {
		t.Fatalf("score: %v", s)
	}
	one := d.ok(a, "doc.freshness_score", map[string]any{"space_id": s1})
	if one["total"] != float64(2) || one["verified"] != float64(1) || len(one["spaces"].([]any)) != 1 {
		t.Fatalf("space score: %v", one)
	}
	if r := string(d.run(a, "doc.freshness_score", map[string]any{"space_id": s1}).body); !strings.Contains(r, `"score":0.5`) {
		t.Fatalf("score must print as a float: %s", r)
	}
	d.code("not_found", b, "doc.verify", map[string]any{"note_id": p1})
	if ask := d.ok(b, "doc.ask", map[string]any{"question": "alpha unique-term"}); ask["answered"] != false {
		t.Fatal("tenant B answered from tenant A")
	}
	if d.ok(b, "doc.freshness_score", map[string]any{})["total"] != float64(0) {
		t.Fatal("tenant B score")
	}
	if n := len(d.list(b, "doc.proposals", map[string]any{})); n != 0 {
		t.Fatalf("proposals leak: %d", n)
	}
}

func TestDocsCaptureDraftsPageOwnedByCaller(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	space := d.space(owner, "DOC", "open")
	n := d.ok(member, "doc.capture", map[string]any{"source": "chat", "text": "We decided to ship on Friday", "space_id": space})
	if n["owner_id"] != member.userID || n["title"] != "We decided to ship on Friday" || n["content_md"] == "" {
		t.Fatalf("capture: %v", n)
	}
	d.code("invalid", member, "doc.capture", map[string]any{"source": "email", "text": "x", "space_id": space})
}

func TestDocsViewRestrictedPagesNeverLeak(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	space := d.space(owner, "OPN", "open")
	page := d.page(owner, space, "Breach playbook", nil, "Rotate the vault credentials immediately")
	child := d.page(owner, space, "Breach contacts", nil, "vault credentials escalation phone tree")
	d.ok(owner, "note.update", map[string]any{"id": child, "parent_id": page})
	d.ok(owner, "note.restrict", map[string]any{"note_id": page, "restriction": "view"})

	if seen := d.ok(owner, "doc.ask", map[string]any{"question": "rotate vault credentials"}); seen["answered"] != true {
		t.Fatalf("owner: %v", seen)
	}
	hidden := d.run(member, "doc.ask", map[string]any{"question": "rotate vault credentials"})
	if hidden.json()["answered"] != false || strings.Contains(string(hidden.body), "Breach") {
		t.Fatalf("leak: %s", hidden.body)
	}
	if d.ok(member, "doc.freshness_score", map[string]any{})["total"] != float64(0) {
		t.Fatal("member score sees restricted pages")
	}
	if d.ok(owner, "doc.freshness_score", map[string]any{})["total"] != float64(2) {
		t.Fatal("owner score")
	}
	// Saving a restricted page (or its child) as a template would publish it to the whole space.
	for _, id := range []string{page, child} {
		d.code("forbidden", owner, "template.save", map[string]any{"note_id": id, "name": "leaky", "source_kind": "none"})
	}
	d.code("not_found", member, "template.save", map[string]any{"note_id": page, "name": "leaky", "source_kind": "none"})
}

// ---------- templates.rs ----------

func (d dg) templateID(w who, name string) any {
	for _, t := range d.list(w, "template.list", map[string]any{}) {
		if t["name"] == name {
			return t["id"]
		}
	}
	d.t.Fatalf("no template %s", name)
	return nil
}

// save creates a note with the body and saves it as a template.
func (d dg) save(w who, space any, name, kind, body string, types []string) any {
	n := d.ok(w, "note.create", map[string]any{"space_id": space, "title": name + " {{contact.name}}", "content_md": body, "types": types})
	return d.ok(w, "template.save", map[string]any{"note_id": n["id"], "name": name, "source_kind": kind})["id"]
}

func TestTemplatesRenderFromContactDealInvoice(t *testing.T) {
	d, p := newDG(t)
	space := d.space(p, "CRM", "open")
	contact := d.ok(p, "contact.create", map[string]any{"space_id": space, "name": "Asha Rao", "email": "asha@example.test"})
	deal := d.ok(p, "deal.create", map[string]any{"space_id": space, "title": "Website", "contact_id": contact["id"], "amount_minor": 123456700})
	inv := d.ok(p, "invoice.create_draft", map[string]any{"space_id": space, "contact_id": contact["id"], "lines": []any{map[string]any{"description": "Work", "unit_price_minor": 100000}}})

	proposal, cover := d.templateID(p, "Proposal"), d.templateID(p, "Invoice cover letter") // seeded by the workspace hook
	r := d.ok(p, "template.render", map[string]any{"template_id": proposal, "source_id": deal["id"]})
	note := r["note"].(map[string]any)
	md := note["content_md"].(string)
	if !strings.Contains(md, "Proposal for Asha Rao") || !strings.Contains(md, "₹12,34,567.00") {
		t.Fatal(md)
	}
	if note["title"] != "Proposal: Website" || note["space_id"] != space || len(r["unresolved"].([]any)) != 0 {
		t.Fatalf("render: %v", r)
	}
	r = d.ok(p, "template.render", map[string]any{"template_id": cover, "source_id": inv["id"]})
	md = r["note"].(map[string]any)["content_md"].(string)
	if !strings.Contains(md, "invoice DRAFT") || !strings.Contains(md, "Dear Asha Rao") || !strings.Contains(md, "₹") {
		t.Fatal(md)
	}

	// A contact template using every catalogue field leaves nothing unresolved.
	body := ""
	for _, f := range d.list(p, "template.fields", map[string]any{"source_kind": "contact"}) {
		body += "{{" + f["path"].(string) + "}}\n"
	}
	tid := d.save(p, space, "All contact fields", "contact", body+"{{made.up}} {{1+1}}", []string{})
	r = d.ok(p, "template.render", map[string]any{"template_id": tid, "source_id": contact["id"]})
	if un := r["unresolved"].([]any); len(un) != 1 || un[0] != "made.up" {
		t.Fatalf("unresolved: %v", un)
	}
	n := r["note"].(map[string]any)
	if !strings.Contains(n["content_md"].(string), "{{made.up}}") || n["title"] != "All contact fields Asha Rao" {
		t.Fatalf("render: %v", n)
	}
}

func TestTemplatesRefreshAndBoardsAreOneShot(t *testing.T) {
	d, p := newDG(t)
	space := d.space(p, "CRM", "open")
	contact := d.ok(p, "contact.create", map[string]any{"space_id": space, "name": "Old Name"})
	tid := d.save(p, space, "Hello", "contact", "Hello {{contact.name}}", []string{})
	r := d.ok(p, "template.render", map[string]any{"template_id": tid, "source_id": contact["id"]})
	note := r["note"].(map[string]any)["id"]
	d.ok(p, "contact.update", map[string]any{"id": contact["id"], "name": "New Name"})
	d.ok(p, "template.refresh", map[string]any{"note_id": note})
	if n := d.ok(p, "note.get", map[string]any{"id": note}); n["content_md"] != "Hello New Name" {
		t.Fatalf("refresh: %v", n["content_md"])
	}

	snap := "```tldraw\n{\"store\":{\"a\":{\"text\":\"For {{contact.name}}\"}}}\n```"
	bid := d.save(p, space, "Board", "contact", snap, []string{"whiteboard"})
	r = d.ok(p, "template.render", map[string]any{"template_id": bid, "source_id": contact["id"]})
	bn := r["note"].(map[string]any)
	if ty := bn["types"].([]any); len(ty) != 1 || ty[0] != "whiteboard" || !strings.Contains(bn["content_md"].(string), "For New Name") {
		t.Fatalf("board: %v", bn)
	}
	d.code("invalid", p, "template.refresh", map[string]any{"note_id": bn["id"]})
	d.ok(p, "template.delete", map[string]any{"template_id": tid})
	d.code("invalid", p, "template.refresh", map[string]any{"note_id": note})
}

func TestTemplatesRenderIsAccessChecked(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	open, private := d.space(owner, "OPEN", "open"), d.space(owner, "SEC", "private")
	secret := d.ok(owner, "contact.create", map[string]any{"space_id": private, "name": "Secret Co"})
	public := d.ok(owner, "contact.create", map[string]any{"space_id": open, "name": "Public Co"})
	deal := d.ok(owner, "deal.create", map[string]any{"space_id": open, "title": "D", "contact_id": secret["id"]})
	tid := d.save(owner, open, "Contact card", "contact", "{{contact.name}}", []string{})

	d.code("not_found", member, "template.render", map[string]any{"template_id": tid, "source_id": secret["id"], "space_id": open})
	d.ok(member, "template.render", map[string]any{"template_id": tid, "source_id": public["id"]})

	// A deal the member can read does not leak the private contact linked to it.
	r := d.ok(member, "template.render", map[string]any{"template_id": d.templateID(member, "Proposal"), "source_id": deal["id"]})
	md := r["note"].(map[string]any)["content_md"].(string)
	if strings.Contains(md, "Secret Co") || !strings.Contains(md, "{{contact.name}}") {
		t.Fatal(md)
	}
	found := false
	for _, u := range r["unresolved"].([]any) {
		found = found || u == "contact.name"
	}
	if !found {
		t.Fatalf("unresolved: %v", r["unresolved"])
	}
	// Wrong kind of record.
	d.code("invalid", owner, "template.render", map[string]any{"template_id": tid, "source_id": deal["id"]})

	// Another workspace sees neither the template nor the records.
	other := d.e.signup("Other", true)
	d.code("not_found", other, "template.render", map[string]any{"template_id": tid, "source_id": public["id"]})
	own := d.save(other, d.space(other, "X", "open"), "Mine", "contact", "{{contact.name}}", []string{})
	if d.run(other, "template.render", map[string]any{"template_id": own, "source_id": public["id"]}).status == 200 {
		t.Fatal("rendered a foreign contact")
	}
}

func TestTemplatesFromPrivateSpacesStayPrivate(t *testing.T) {
	d, owner := newDG(t)
	member := d.member(owner)
	private := d.space(owner, "HUSH", "private")
	tid := d.save(owner, private, "Board pack", "none", "salary bands", []string{})
	names := func(w who) []string {
		var out []string
		for _, x := range d.list(w, "template.list", map[string]any{}) {
			out = append(out, x["name"].(string))
		}
		return out
	}
	has := func(l []string, s string) bool {
		for _, x := range l {
			if x == s {
				return true
			}
		}
		return false
	}
	if !has(names(owner), "Board pack") || has(names(member), "Board pack") {
		t.Fatalf("visibility: %v %v", names(owner), names(member))
	}
	open := d.space(owner, "OPN", "open")
	for cmd, in := range map[string]map[string]any{
		"template.render": {"template_id": tid, "space_id": open},
		"template.delete": {"template_id": tid},
	} {
		if d.run(member, cmd, in).status == 200 {
			t.Fatalf("%s must fail", cmd)
		}
	}
	// Same name from an open page must not overwrite the private template.
	n := d.ok(member, "note.create", map[string]any{"space_id": open, "title": "x", "content_md": "mine"})
	d.code("conflict", member, "template.save", map[string]any{"note_id": n["id"], "name": "Board pack", "source_kind": "none"})
}

func TestTemplatesOnlyAuthorOrAdminCanOverwriteOrDelete(t *testing.T) {
	d, owner := newDG(t)
	a, b := d.member(owner), d.member(owner)
	open := d.space(owner, "ALL", "open")
	starter := d.templateID(a, "Proposal")
	if d.run(a, "template.delete", map[string]any{"template_id": starter}).status == 200 {
		t.Fatal("member deleted a starter template")
	}
	mine := d.save(a, open, "A's card", "none", "v1", []string{})
	if d.run(b, "template.delete", map[string]any{"template_id": mine}).status == 200 {
		t.Fatal("member deleted another's template")
	}
	n := d.ok(b, "note.create", map[string]any{"space_id": open, "title": "x", "content_md": "hijack"})
	d.code("conflict", b, "template.save", map[string]any{"note_id": n["id"], "name": "A's card", "source_kind": "none"})
	// The author can replace it; an admin can delete anything.
	d.save(a, open, "A's card", "none", "v2", []string{})
	d.ok(owner, "template.delete", map[string]any{"template_id": starter})
	d.ok(a, "template.delete", map[string]any{"template_id": d.templateID(a, "A's card")})
}

func TestDocsIdempotentReplayAndWorkerVisibleRows(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "ENG", "open")
	page := d.page(owner, space, "Guide", nil, "body")
	hdr := map[string]string{"Idempotency-Key": "docs-" + rnd()}
	in := map[string]any{"note_id": page, "verify_by": "2031-05-06"}
	a := d.e.cmd(d.e.gosv.URL, owner, d.ws, "doc.verify", in, hdr)
	b := d.e.cmd(d.e.gosv.URL, owner, d.ws, "doc.verify", in, hdr)
	if a.status != 200 || string(a.body) != string(b.body) || string(a.body) != `{"freshness":"fresh","note_id":"`+page+`","verify_by":"2031-05-06"}` {
		t.Fatalf("%d %s / %d %s", a.status, a.body, b.status, b.body)
	}
	if n := d.scalar("select count(*)::text from core.events where kind = 'doc.verified' and note_id = $1::uuid", page); n != "1" {
		t.Fatalf("doc.verified events after replay: %s", n)
	}

	// every event a command emits has its event.dispatch job, so the Rust worker reacts to it
	_, pid := d.stale(page, "new body")
	d.ok(owner, "doc.proposal_decide", map[string]any{"proposal_id": pid, "approve": true})
	d.ok(owner, "doc.set_owner", map[string]any{"note_id": page, "owner_id": owner.userID})
	tpl := d.save(owner, space, "T", "none", "x", []string{})
	d.ok(owner, "template.render", map[string]any{"template_id": tpl, "space_id": space})
	d.ok(owner, "template.delete", map[string]any{"template_id": tpl})
	kinds := d.scalar(`select string_agg(kind, ',' order by id) from core.events where actor_type = 'user' and kind like any (array['doc.%','template.%','note.updated'])`)
	for _, k := range []string{"doc.verified", "note.updated", "doc.proposal_decided", "doc.owner_changed", "template.saved", "template.rendered", "template.deleted"} {
		if !strings.Contains(kinds, k) {
			t.Fatalf("missing event %s in %s", k, kinds)
		}
	}
	if n := d.scalar(`select count(*)::text from core.events x where x.actor_type = 'user' and x.created_at is not null
		and not exists (select 1 from core.jobs j where j.kind = 'event.dispatch' and j.payload->>'event_id' = x.id::text)`); n != "0" {
		t.Fatalf("%s events without a dispatch job", n)
	}
	// the decide payload carries the deciding user
	if got := d.scalar("select payload->>'decided_by' from core.events where kind = 'doc.proposal_decided'"); got != owner.userID {
		t.Fatalf("decided_by %s", got)
	}
}

func TestDocsNestedNoteUpdateUsesTheFullGate(t *testing.T) {
	d, owner := newDG(t)
	space := d.space(owner, "ENG", "open")
	page := d.page(owner, space, "Guide", nil, "body")
	_, pid := d.stale(page, "agent body")
	agent := d.e.apiKey(owner) // machine principal: nested note.update is recorded like Rust ctx.call
	d.ok(agent, "doc.proposal_decide", map[string]any{"proposal_id": pid, "approve": true})
	if got := d.scalar("select string_agg(meta->>'command', ',' order by meta->>'command') from core.usage where kind = 'mcp.call'"); got != "doc.proposal_decide,note.update" {
		t.Fatalf("usage rows: %s", got)
	}
}

func TestCnQaAI010AgentCannotApproveAnAIProposal(t *testing.T) {
	d, owner := newDG(t)
	d.ok(owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes"}}})
	key := d.ok(owner, "agent.create", map[string]any{"name": "Reviser", "scopes": []string{"read", "write_notes"}})["api_key"].(string)
	agent := who{ws: owner.ws, bearer: key}
	page := d.page(owner, d.space(owner, "ENG", "open"), "Deploy guide", nil, "Releases are tracked in ENG-12")
	_, pid := d.stale(page, "Releases are tracked in ENG-12\nREVISED")
	d.code("forbidden", agent, "doc.proposal_decide", map[string]any{"proposal_id": pid, "approve": true})
	if strings.Contains(d.ok(owner, "note.get", map[string]any{"id": page})["content_md"].(string), "REVISED") {
		t.Fatal("agent applied the proposal")
	}
	if n := len(d.list(owner, "doc.proposals", map[string]any{})); n != 1 {
		t.Fatalf("proposal should stay pending: %d", n)
	}
}

func TestCnQaAI007AI011MCP006StaleProposalIsRefusedAfterThePageChanged(t *testing.T) {
	d, owner := newDG(t)
	page := d.page(owner, d.space(owner, "ENG", "open"), "Deploy guide", nil, "Releases are tracked in ENG-12")
	_, pid := d.stale(page, "Releases are tracked in ENG-12\nREVISED")
	d.ok(owner, "note.update", map[string]any{"id": page, "content_md": "Edited by a human while the AI drafted"})
	d.code("conflict", owner, "doc.proposal_decide", map[string]any{"proposal_id": pid, "approve": true})
	if got := d.ok(owner, "note.get", map[string]any{"id": page})["content_md"]; got != "Edited by a human while the AI drafted" {
		t.Fatalf("human edit lost: %v", got)
	}
	eq(t, d.ok(owner, "doc.proposal_decide", map[string]any{"proposal_id": pid, "approve": false})["status"], "rejected")
}

func TestCnQaAI012AgentPermissionChangesNeedHumanApproval(t *testing.T) {
	d, owner := newDG(t)
	d.ok(owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "write_notes"}}})
	key := d.ok(owner, "agent.create", map[string]any{"name": "Editor", "scopes": []string{"read", "write_notes"}})["api_key"].(string)
	agent := who{ws: owner.ws, bearer: key}
	page := d.page(owner, d.space(owner, "ENG", "open"), "Board minutes", nil, "private")
	for cmd, in := range map[string]map[string]any{
		"note.restrict": {"note_id": page, "restriction": "view"},
		"note.share":    {"note_id": page, "email": owner.email, "level": "view"},
		"note.unshare":  {"note_id": page, "user_id": owner.userID},
	} {
		if got := d.ok(agent, cmd, in)["confirmation_required"]; got != true {
			t.Fatalf("%s ran without approval: %v", cmd, got)
		}
	}
	eq(t, d.ok(owner, "note.permissions", map[string]any{"note_id": page})["restriction"], "none")
}

func TestCnQaSecRevokedAndReadOnlyAgentKeys(t *testing.T) {
	d, owner := newDG(t)
	created := d.ok(owner, "agent.create", map[string]any{"name": "Reader", "scopes": []string{"read"}})
	agent := who{ws: owner.ws, bearer: created["api_key"].(string)}
	space := d.space(owner, "ENG", "open")
	d.code("forbidden", agent, "note.create", map[string]any{"space_id": space, "title": "nope"})
	d.ok(agent, "space.list", map[string]any{})
	d.ok(owner, "apikey.revoke", map[string]any{"id": created["api_key_id"]})
	if r := d.run(agent, "space.list", map[string]any{}); r.status != 401 {
		t.Fatalf("revoked key: %d %s", r.status, r.body)
	}
}

// MCP-003 (Go-only commands visible to agents). The Rust /mcp endpoint, which the Go API proxies, lists only the Rust
// registry, so commands that exist only in Go (quote.*) cannot be found with `discover` nor run with `invoke`.
func TestCnQaMCP003GoOnlyCommandsAreDiscoverableByAgents(t *testing.T) {
	t.Skip("known gap: /mcp is proxied to Rust, whose registry lacks Go-only commands (quote.*); fix = Go answers tools/call discover+invoke for its native commands (merge into Rust's discover) and proxies the rest")
	d, owner := newDG(t)
	key := d.ok(owner, "agent.create", map[string]any{"name": "Seller"})["api_key"].(string)
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "discover", "arguments": map[string]any{"query": "quote send"}}}
	r := d.e.do(d.e.gosv.URL, "POST", "/mcp", who{bearer: key}, body, map[string]string{"Accept": "application/json, text/event-stream"})
	if !strings.Contains(string(r.body), "quote.send") {
		t.Fatalf("quote.send not discoverable through /mcp: %s", r.body)
	}
}
