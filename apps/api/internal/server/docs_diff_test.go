package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Twin-fixture differential test for the docs commands (living pages, capture requests, templates): the same
// script runs against fixture A through Rust and fixture B through Go; normalized responses, resulting docs/note
// rows, emitted events, dispatch jobs and usage rows must be equal. Afterwards every read command is compared raw
// (bytes) between the two servers on each fixture for every caller.

func docsScript(r *runner) {
	e, ws, sp := r.e, r.f.owner.ws, r.f.space
	sc := func(q string, a ...any) string { return docsScalar(e, ws, q, a...) }
	ex := func(q string, a ...any) { adminSQL(e, ws, q, a...) }
	mem, bob, carol, own := r.f.member.userID, r.f.bob.userID, r.f.carol.userID, r.f.owner.userID
	ph := "00000000-0000-4000-8000-000000000001"
	set := func(name, v string) string { r.vars[name] = v; return v }

	// ---- fixtures
	r.id("prv", "owner", "space.create", map[string]any{"key": "PRV", "name": "Private", "visibility": "private"})
	r.id("opn", "owner", "space.create", map[string]any{"key": "OPN", "name": "Open", "visibility": "open"})
	page := func(name, caller string, space any, title, md string) {
		r.id(name, caller, "note.create", map[string]any{"space_id": space, "title": title, "content_md": md})
	}
	page("p1", "owner", sp, "Handbook", "hello <b>&</b> ünï 🚀")
	page("p2", "member", sp, "Member page", "member owned")
	page("p3", "bob", sp, "Bob page", "bob owned")
	page("p4", "owner", sp, "Replacement char", "old")
	page("pp", "owner", r.v("prv"), "Secret page", "secret body")
	page("rest", "owner", sp, "Restricted", "restricted body")
	r.call("owner", "note.restrict", map[string]any{"note_id": r.v("rest"), "restriction": "view"})
	page("kid", "owner", sp, "Kid", "kid body")
	r.call("owner", "note.update", map[string]any{"id": r.v("kid"), "parent_id": r.v("rest")})
	page("pa1", "owner", sp, "Archived one", "zebra one")
	page("pa2", "member", sp, "Archived two", "zebra two")
	page("pa3", "owner", r.v("prv"), "Archived private", "zebra three")
	page("old", "owner", sp, "Unverified", "u")
	r.id("task", "owner", "note.create", map[string]any{"space_id": sp, "title": "Ship it", "types": []string{"task"}})
	ex(`insert into projects.items (note_id, workspace_id, space_id, number, key, status, priority, item_type, due_date, assignee_id)
	    values ($1::uuid,$2::uuid,$3::uuid,12,'ENG-12','todo','high','bug','2027-03-04',$4::uuid)`, r.v("task"), ws, sp, mem)
	r.id("task2", "owner", "note.create", map[string]any{"space_id": sp, "title": "Unassigned", "types": []string{"task"}})
	ex(`insert into projects.items (note_id, workspace_id, space_id, number, key, status) values ($1::uuid,$2::uuid,$3::uuid,13,'ENG-13','open')`, r.v("task2"), ws, sp)
	snap := "```tldraw\n{\"store\":{\"a\":{\"text\":\"For {{contact.name}} \\\"q\\\" {{x.y}}\",\"n\":[1,2.5,-3]}}}\n```"
	r.id("board", "owner", "note.create", map[string]any{"space_id": sp, "title": "Board {{contact.name}}", "types": []string{"whiteboard"}, "content_md": snap})
	r.id("design", "owner", "note.create", map[string]any{"space_id": sp, "title": "Design", "types": []string{"design"}, "content_md": "no fence {{contact.email}}"})
	r.id("badboard", "owner", "note.create", map[string]any{"space_id": sp, "title": "Bad", "types": []string{"whiteboard"}, "content_md": "```tldraw\nnot json\n```"})
	r.id("expboard", "owner", "note.create", map[string]any{"space_id": sp, "title": "Exp", "types": []string{"whiteboard"}, "content_md": "```tldraw\n{\"x\":1e30}\n```"})

	r.id("c1", "owner", "contact.create", map[string]any{"space_id": sp, "name": "Ásha \"Rao\" 🚀", "email": "a@example.test", "phone": "+91 99999", "company": "Acme & Co"})
	ex("update crm.contacts set billing_address = E'1 MG Road\\nPune {{x}}', gstin = '27AAPFU0939F1ZV' where note_id = $1::uuid", r.v("c1"))
	r.id("c0", "owner", "contact.create", map[string]any{"space_id": sp, "name": "Bare"})
	r.id("c2", "owner", "contact.create", map[string]any{"space_id": r.v("prv"), "name": "Secret Co"})
	r.id("d1", "owner", "deal.create", map[string]any{"space_id": sp, "title": "Website", "contact_id": r.v("c1"), "amount_minor": 123456700, "expected_close": "2027-01-05", "probability": 40, "stage": "proposal"})
	r.id("d2", "owner", "deal.create", map[string]any{"space_id": sp, "title": "Leaky", "contact_id": r.v("c2")})
	r.id("d3", "owner", "deal.create", map[string]any{"space_id": sp, "title": "Dollar", "currency": "USD", "amount_minor": 123456789})
	r.id("d4", "owner", "deal.create", map[string]any{"space_id": sp, "title": "Tiny", "amount_minor": 5, "contact_id": r.v("c0")})
	r.id("i1", "owner", "invoice.create_draft", map[string]any{"space_id": sp, "contact_id": r.v("c1"), "lines": []any{map[string]any{"description": "Work", "unit_price_minor": 100000}}})

	// seeded worker output: flags, proposals, archived pages, capture requests, an unverified import
	flag := func(page, reason string) string {
		return sc("insert into docs.freshness_flags (workspace_id, note_id, reason) values ($1::uuid,$2::uuid,$3) returning id::text", ws, page, reason)
	}
	prop := func(name, page, flagID, md string) {
		var f any
		if flagID != "" {
			f = flagID
		}
		set(name, sc(`insert into docs.edit_proposals (workspace_id, note_id, flag_id, proposed_md, rationale)
			values ($1::uuid,$2::uuid,$3::uuid,$4,'because ünï') returning id::text`, ws, page, f, md))
		time.Sleep(5 * time.Millisecond)
	}
	for _, p := range []string{"p1", "p2", "p3", "p4", "pp"} {
		ex("update core.notes set freshness = 'stale' where id = $1::uuid", r.v(p))
	}
	prop("pr1", r.v("p1"), flag(r.v("p1"), "binding_changed"), "revised p1\nREVISED")
	flag(r.v("p1"), "imported")
	prop("pr2", r.v("p2"), flag(r.v("p2"), "binding_changed"), "revised p2")
	prop("pr3", r.v("p3"), flag(r.v("p3"), "date_lapsed"), "")
	prop("pr4", r.v("p4"), flag(r.v("p4"), "binding_changed"), "has � replacement")
	prop("pr5", r.v("pp"), flag(r.v("pp"), "binding_changed"), "private revised")
	prop("pr6", r.v("p2"), "", "second proposal without a flag")
	ex("update core.notes set owner_id = $2::uuid, lapse_count = 1 where id = $1::uuid", r.v("p3"), bob)
	ex("update core.notes set owner_id = $2::uuid where id = $1::uuid", r.v("p2"), mem)
	for _, a := range []string{"pa1", "pa2", "pa3"} {
		ex("update core.notes set freshness = 'archived', archived_at = now() - interval '1 day' * $2, lapse_count = 2 where id = $1::uuid", r.v(a), len(a))
	}
	ex("update core.notes set owner_id = $2::uuid where id = $1::uuid", r.v("pa2"), carol)
	ex("update core.notes set freshness = 'unverified' where id = $1::uuid", r.v("old"))
	cap := func(name, q string, asked, routed any) {
		set(name, sc("insert into docs.capture_requests (workspace_id, question, asked_by, routed_to) values ($1::uuid,$2,$3::uuid,$4::uuid) returning id::text", ws, q, asked, routed))
		time.Sleep(5 * time.Millisecond)
	}
	cap("cr1", "How do we deploy? ünï", mem, own)
	cap("cr2", "Who owns billing?", bob, mem)
	cap("cr3", "Orphan question", nil, nil)
	cap("cr4", "Dismiss me", carol, carol)
	ex("update docs.capture_requests set status = 'dismissed' where id = $1::uuid", r.v("cr4"))

	callers := []string{"owner", "member", "bob", "guest", "agent", "apikey", "other", "noauth", "badkey"}

	// ---- doc.verify
	for _, c := range callers {
		r.call(c, "doc.verify", map[string]any{"note_id": r.v("p1")})
	}
	r.call("owner", "doc.verify", map[string]any{"note_id": r.v("p1"), "verify_by": "2031-02-03"})
	r.call("member", "doc.verify", map[string]any{"note_id": r.v("p2"), "verify_by": "2030-01-01"})
	r.call("bob", "doc.verify", map[string]any{"note_id": r.v("p3"), "verify_by": nil})
	r.call("owner", "doc.verify", map[string]any{"note_id": r.v("old")})
	for _, d := range []string{"2000-01-01", time.Now().UTC().Format("2006-01-02"), "0000-01-01"} {
		r.call("owner", "doc.verify", map[string]any{"note_id": r.v("p1"), "verify_by": d})
	}
	r.call("owner", "doc.verify", map[string]any{"note_id": r.v("task")})
	r.call("owner", "doc.verify", map[string]any{"note_id": r.v("pp")})
	r.call("owner", "doc.verify", map[string]any{"note_id": ph})
	r.call("member", "doc.verify", map[string]any{"note_id": r.v("pp")})
	r.call("member", "doc.verify", map[string]any{"note_id": r.v("rest")})
	for _, raw := range []string{`{}`, `[]`, `null`, `{bad`, `{"note_id":"nope"}`, `{"note_id":null}`, `{"note_id":5}`,
		`{"note_id":"` + r.v("p1") + `","verify_by":"2031-13-40"}`, `{"note_id":"` + r.v("p1") + `","verify_by":"2031-2-3"}`,
		`{"note_id":"` + strings.ToUpper(r.v("p1")) + `","verify_by":"2032-01-01"}`, `{"Note_Id":"` + r.v("p1") + `"}`,
		`{"note_id":"` + r.v("p1") + `","verify_by":5}`, `{"note_id":"` + r.v("p1") + `","extra":[1,2]}`} {
		r.raw("owner", "doc.verify", raw)
	}

	// ---- doc.set_owner
	r.call("owner", "doc.set_owner", map[string]any{"note_id": r.v("p1"), "owner_id": mem})
	r.call("bob", "doc.set_owner", map[string]any{"note_id": r.v("p1"), "owner_id": bob})
	r.call("member", "doc.set_owner", map[string]any{"note_id": r.v("p1"), "owner_id": mem})
	r.call("member", "doc.set_owner", map[string]any{"note_id": r.v("p1"), "owner_id": ph})
	r.call("member", "doc.set_owner", map[string]any{"note_id": r.v("p1"), "owner_id": r.f.other.userID})
	r.call("member", "doc.set_owner", map[string]any{"note_id": r.v("p1"), "owner_id": own})
	r.call("owner", "doc.set_owner", map[string]any{"note_id": r.v("task"), "owner_id": mem})
	r.call("owner", "doc.set_owner", map[string]any{"note_id": ph, "owner_id": mem})
	for _, c := range []string{"guest", "agent", "apikey", "other", "noauth", "badkey"} {
		r.call(c, "doc.set_owner", map[string]any{"note_id": r.v("p3"), "owner_id": bob})
	}
	r.call("owner", "doc.set_owner", map[string]any{"note_id": r.v("p3"), "owner_id": carol})
	r.raw("owner", "doc.set_owner", `{"note_id":"`+r.v("p3")+`"}`)
	r.raw("owner", "doc.set_owner", `{"note_id":"`+r.v("p3")+`","owner_id":"x"}`)

	// ---- doc.restore
	for _, c := range []string{"bob", "guest", "other", "noauth", "badkey"} {
		r.call(c, "doc.restore", map[string]any{"note_id": r.v("pa1")})
	}
	r.call("owner", "doc.restore", map[string]any{"note_id": r.v("pa1")})
	r.call("owner", "doc.restore", map[string]any{"note_id": r.v("pa1")})
	r.call("owner", "doc.restore", map[string]any{"note_id": r.v("p4")})
	r.call("carol", "doc.restore", map[string]any{"note_id": r.v("pa2")})
	r.call("agent", "doc.restore", map[string]any{"note_id": r.v("pa3")})
	r.call("owner", "doc.restore", map[string]any{"note_id": r.v("task")})
	r.call("owner", "doc.restore", map[string]any{"note_id": ph})
	r.raw("owner", "doc.restore", `{}`)
	r.raw("owner", "doc.restore", `{"note_id":"x"}`)
	r.call("owner", "doc.archived", map[string]any{})

	// ---- doc.proposal_decide
	r.call("bob", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr2"), "approve": true})
	r.call("guest", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr2"), "approve": true})
	r.call("other", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr2"), "approve": true})
	r.call("noauth", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr2"), "approve": true})
	r.call("badkey", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr2"), "approve": true})
	r.call("owner", "doc.proposal_decide", map[string]any{"proposal_id": ph, "approve": true})
	r.call("owner", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr1"), "approve": true})
	r.call("owner", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr1"), "approve": true})
	r.call("owner", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr1"), "approve": false})
	r.call("member", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr2"), "approve": false})
	r.call("member", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr6"), "approve": true}) // member owns p2: approve, still stale? other flags resolved
	r.call("bob", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr3"), "approve": true})    // empty proposed_md
	r.call("agent", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr5"), "approve": true})
	r.call("apikey", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr4"), "approve": true}) // U+FFFD content: Go hands the request to Rust
	r.call("owner", "doc.proposal_decide", map[string]any{"proposal_id": r.v("pr4"), "approve": true})
	r.raw("owner", "doc.proposal_decide", `{"proposal_id":"`+r.v("pr4")+`"}`)
	r.raw("owner", "doc.proposal_decide", `{"proposal_id":"`+r.v("pr4")+`","approve":"yes"}`)
	r.raw("owner", "doc.proposal_decide", `{"proposal_id":"`+r.v("pr4")+`","approve":null}`)
	r.raw("owner", "doc.proposal_decide", `{"proposal_id":"bad","approve":true}`)

	// ---- capture.answer
	r.call("carol", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": r.v("p3")})
	r.call("guest", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": r.v("p3")})
	r.call("other", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": r.v("p3")})
	r.call("noauth", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": r.v("p3")})
	r.call("badkey", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": r.v("p3")})
	r.call("bob", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": r.v("pp")}) // private page
	r.call("bob", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": ph})
	r.call("bob", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": r.v("p3")})
	r.call("bob", "capture.answer", map[string]any{"request_id": r.v("cr2"), "note_id": r.v("p3")})
	r.call("member", "capture.answer", map[string]any{"request_id": r.v("cr1"), "note_id": r.v("p2")})
	r.call("owner", "capture.answer", map[string]any{"request_id": r.v("cr3"), "note_id": r.v("pp")})
	r.call("owner", "capture.answer", map[string]any{"request_id": r.v("cr4"), "note_id": r.v("pp")})
	r.call("owner", "capture.answer", map[string]any{"request_id": ph, "note_id": r.v("pp")})
	r.raw("owner", "capture.answer", `{"request_id":"`+r.v("cr3")+`"}`)

	// ---- template.fields
	for _, k := range []string{"none", "contact", "deal", "invoice", "item", "", "bogus", "Contact"} {
		r.call("member", "template.fields", map[string]any{"source_kind": k})
	}
	for _, c := range []string{"guest", "agent", "apikey", "other", "noauth", "badkey"} {
		r.call(c, "template.fields", map[string]any{"source_kind": "deal"})
	}
	r.raw("owner", "template.fields", `{}`)
	r.raw("owner", "template.fields", `{"source_kind":null}`)

	// ---- template.save
	save := func(name, caller, note, tname, kind string) {
		r.id(name, caller, "template.save", map[string]any{"note_id": r.v(note), "name": tname, "source_kind": kind})
	}
	save("t1", "owner", "p3", "Plain", "none")
	save("tc", "owner", "p1", "Contact card", "contact")
	r.call("owner", "note.update", map[string]any{"id": r.v("p2"), "title": "Card {{contact.name}} {{contact.email}} {{contact.phone}} {{contact.company}} {{contact.gstin}} {{contact.address}} {{ today }} {{workspace.name}} {{user.name}}", "content_md": "{{contact.name}}\n{{ contact.company }}\n{{contact.address}}\n{{user.name}}|{{workspace.name}}|{{today}}\n{{made.up}} {{1+1}} {{a.b.c}} {{ x }}"})
	save("tcc", "member", "p2", "All contact fields", "contact")
	save("td", "owner", "p3", "Deal sheet", "deal")
	save("ti", "owner", "p3", "Invoice sheet", "invoice")
	save("tt", "owner", "p3", "Item card", "item")
	save("tb", "owner", "board", "Board", "contact")
	save("tbd", "owner", "design", "Design board", "contact")
	save("tbad", "owner", "badboard", "Bad board", "contact")
	save("texp", "owner", "expboard", "Exp board", "contact")
	save("tu", "owner", "p3", " Ünï 🚀 name ", "none")
	save("tl", "owner", "p3", strings.Repeat("n", 300), "none")
	r.call("owner", "template.save", map[string]any{"note_id": r.v("p3"), "name": "Plain", "source_kind": "none"}) // author overwrites
	r.call("owner", "template.save", map[string]any{"note_id": r.v("p3"), "name": "Contact card", "source_kind": "deal"})
	r.call("bob", "template.save", map[string]any{"note_id": r.v("p3"), "name": "Contact card", "source_kind": "none"}) // not the author
	r.call("bob", "template.save", map[string]any{"note_id": r.v("p3"), "name": "Proposal", "source_kind": "none"})     // seeded starter
	r.call("owner", "template.save", map[string]any{"note_id": r.v("p3"), "name": "Proposal", "source_kind": "deal"})   // admin overwrite
	r.call("owner", "template.save", map[string]any{"note_id": r.v("pp"), "name": "Private tpl", "source_kind": "none"})
	r.call("member", "template.save", map[string]any{"note_id": r.v("pp"), "name": "Private tpl 2", "source_kind": "none"})
	r.call("member", "template.save", map[string]any{"note_id": r.v("pp"), "name": "Private tpl", "source_kind": "none"})
	r.call("owner", "template.save", map[string]any{"note_id": r.v("p3"), "name": "Private tpl", "source_kind": "none"}) // admin may replace
	for _, n := range []string{"rest", "kid"} {
		r.call("owner", "template.save", map[string]any{"note_id": r.v(n), "name": "leaky", "source_kind": "none"})
	}
	r.call("member", "template.save", map[string]any{"note_id": r.v("rest"), "name": "leaky", "source_kind": "none"})
	r.call("owner", "template.save", map[string]any{"note_id": r.v("task"), "name": "typed", "source_kind": "none"})
	for _, in := range []map[string]any{
		{"note_id": r.v("p3"), "name": "", "source_kind": "none"}, {"note_id": r.v("p3"), "name": " \t 　", "source_kind": "none"},
		{"note_id": r.v("p3"), "name": "x", "source_kind": "bogus"}, {"note_id": r.v("p3"), "name": "x", "source_kind": ""},
		{"note_id": ph, "name": "x", "source_kind": "none"}, {"note_id": r.v("p3"), "name": nil, "source_kind": "none"},
		{"note_id": r.v("p3"), "name": 5, "source_kind": "none"}, {"name": "x", "source_kind": "none"},
		{"note_id": "bad", "name": "x", "source_kind": "none"},
	} {
		r.call("owner", "template.save", in)
	}
	for _, c := range []string{"guest", "agent", "apikey", "other", "noauth", "badkey"} {
		r.call(c, "template.save", map[string]any{"note_id": r.v("p3"), "name": "by " + c, "source_kind": "none"})
	}
	r.raw("owner", "template.save", `[]`)

	// ---- template.render
	listIDs := func(caller string) map[string]string {
		res := r.e.cmd(r.base, r.f.who(caller), ws, "template.list", map[string]any{}, nil)
		out := map[string]string{}
		for _, t := range res.jsonList() {
			out[t["name"].(string)] = t["id"].(string)
		}
		return out
	}
	ids := listIDs("owner")
	rend := func(name, caller, tid string, in map[string]any) map[string]any {
		in["template_id"] = tid
		m := r.call(caller, "template.render", in)
		if n, ok := m["note"].(map[string]any); ok {
			r.vars[name] = n["id"].(string)
		}
		return m
	}
	rend("n1", "owner", ids["Proposal"], map[string]any{"source_id": r.v("d1")})
	rend("n2", "member", ids["Proposal"], map[string]any{"source_id": r.v("d1"), "title": "  Custom ünï  "})
	rend("n3", "owner", ids["Proposal"], map[string]any{"source_id": r.v("d2")})
	rend("n4", "member", ids["Proposal"], map[string]any{"source_id": r.v("d2"), "space_id": r.v("opn")})
	rend("n5", "owner", ids["Proposal"], map[string]any{"source_id": r.v("d3"), "title": "   "})
	rend("n6", "owner", ids["Proposal"], map[string]any{"source_id": r.v("d4")})
	rend("n7", "owner", ids["Invoice cover letter"], map[string]any{"source_id": r.v("i1")})
	rend("n8", "owner", ids["Contact card"], map[string]any{"source_id": r.v("c1")})
	rend("n9", "member", ids["All contact fields"], map[string]any{"source_id": r.v("c1")})
	rend("n10", "owner", ids["All contact fields"], map[string]any{"source_id": r.v("c0")})
	rend("n11", "owner", ids["Item card"], map[string]any{"source_id": r.v("task")})
	rend("n12", "owner", ids["Item card"], map[string]any{"source_id": r.v("task2"), "space_id": r.v("opn")})
	rend("n13", "owner", ids["Board"], map[string]any{"source_id": r.v("c1")})
	rend("n14", "owner", ids["Design board"], map[string]any{"source_id": r.v("c1")})
	rend("n15", "owner", ids["Plain"], map[string]any{"space_id": r.v("opn")})
	rend("n16", "owner", ids[strings.Repeat("n", 300)], map[string]any{"space_id": r.v("opn"), "title": ""})
	rend("n17", "owner", ids["Bad board"], map[string]any{"source_id": r.v("c1")})
	rend("n18", "owner", ids["Exp board"], map[string]any{"source_id": r.v("c1")}) // exponent numbers: Go hands over to Rust
	rend("", "owner", ids["Plain"], map[string]any{})
	rend("", "owner", ids["Plain"], map[string]any{"source_id": r.v("c1")})
	rend("", "owner", ids["Proposal"], map[string]any{})
	rend("", "owner", ids["Proposal"], map[string]any{"source_id": r.v("c1")}) // wrong kind
	rend("", "owner", ids["Proposal"], map[string]any{"source_id": ph})
	rend("", "owner", ids["Proposal"], map[string]any{"source_id": r.v("d1"), "space_id": ph})
	rend("", "owner", ids["Contact card"], map[string]any{"source_id": r.v("c2")})
	rend("", "member", ids["Contact card"], map[string]any{"source_id": r.v("c2"), "space_id": r.v("opn")})
	rend("", "owner", ids["Contact card"], map[string]any{"source_id": r.v("d1")})
	rend("", "owner", ph, map[string]any{"source_id": r.v("c1")})
	rend("", "other", ids["Proposal"], map[string]any{"source_id": r.v("d1")})
	rend("", "member", ids["Private tpl"], map[string]any{"space_id": r.v("opn")})
	for _, c := range []string{"guest", "agent", "apikey", "noauth", "badkey"} {
		rend("", c, ids["Proposal"], map[string]any{"source_id": r.v("d1")})
	}
	for _, raw := range []string{`{}`, `[]`, `{"template_id":"` + ids["Plain"] + `","source_id":5}`, `{"template_id":"` + ids["Plain"] + `","space_id":null,"title":null}`,
		`{"template_id":"` + ids["Plain"] + `","title":5,"space_id":"` + r.v("opn") + `"}`} {
		r.raw("owner", "template.render", raw)
	}

	// ---- template.refresh
	r.call("owner", "contact.update", map[string]any{"id": r.v("c1"), "name": "Renamed ünï"})
	r.call("owner", "template.refresh", map[string]any{"note_id": r.v("n8")})
	r.call("owner", "template.refresh", map[string]any{"note_id": r.v("n1")})
	r.call("member", "template.refresh", map[string]any{"note_id": r.v("n2")})
	r.call("bob", "template.refresh", map[string]any{"note_id": r.v("n7")})
	r.call("owner", "template.refresh", map[string]any{"note_id": r.v("n13")}) // board
	r.call("owner", "template.refresh", map[string]any{"note_id": r.v("p1")})  // not from a template
	r.call("owner", "template.refresh", map[string]any{"note_id": r.v("n15")})
	r.call("owner", "template.refresh", map[string]any{"note_id": ph})
	r.call("owner", "template.refresh", map[string]any{"note_id": r.v("n3")}) // source in a private space
	for _, c := range []string{"guest", "agent", "apikey", "other", "noauth", "badkey"} {
		r.call(c, "template.refresh", map[string]any{"note_id": r.v("n8")})
	}
	r.raw("owner", "template.refresh", `{}`)

	// ---- template.delete
	r.call("owner", "template.delete", map[string]any{"template_id": ids["Contact card"]})
	r.call("owner", "template.refresh", map[string]any{"note_id": r.v("n8")}) // template deleted
	r.call("bob", "template.delete", map[string]any{"template_id": ids["All contact fields"]})
	r.call("member", "template.delete", map[string]any{"template_id": ids["All contact fields"]})
	r.call("member", "template.delete", map[string]any{"template_id": ids["All contact fields"]})
	r.call("bob", "template.delete", map[string]any{"template_id": ids["Private tpl"]})
	r.call("owner", "template.delete", map[string]any{"template_id": ph})
	r.call("other", "template.delete", map[string]any{"template_id": ids["Plain"]})
	for _, c := range []string{"guest", "agent", "apikey", "noauth", "badkey"} {
		r.call(c, "template.delete", map[string]any{"template_id": ids["Plain"]})
	}
	r.raw("owner", "template.delete", `{}`)
	r.raw("owner", "template.delete", `{"template_id":"x"}`)
}

// docsDump reads the rows the docs commands touch, normalized, in a deterministic order.
func (r *runner) docsDump() []string {
	r.e.t.Helper()
	tx, err := dbTenant(r.e, r.f.owner.ws)
	if err != nil {
		r.e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var out []string
	for _, q := range []struct{ name, sql string }{
		{"templates", "select to_jsonb(x)::text from docs.templates x order by x.name"},
		{"bindings", "select (to_jsonb(b) - 'note_id')::text || ' ' || n.title from docs.template_bindings b join core.notes n on n.id = b.note_id left join core.notes s on s.id = b.source_id order by n.title, n.content_md, b.source_kind, coalesce(s.title, ''), n.created_at"},
		{"flags", "select (to_jsonb(f) - 'source_event_id')::text || ' ' || n.title from docs.freshness_flags f join core.notes n on n.id = f.note_id order by n.title, f.reason, f.created_at"},
		{"proposals", "select to_jsonb(p)::text from docs.edit_proposals p order by p.proposed_md, p.created_at"},
		{"captures", "select to_jsonb(c)::text from docs.capture_requests c order by c.question"},
		{"notes", "select (to_jsonb(n) - 'search')::text from core.notes n order by n.title, n.created_at, n.content_md"},
		{"relations", "select to_jsonb(x)::text from core.relations x order by x.kind, x.created_at"},
		{"revisions", "select to_jsonb(x)::text from core.note_revisions x order by x.created_at, x.number"},
		{"events", "select (to_jsonb(x) - 'id')::text from core.events x where x.actor_type in ('user','agent') and x.kind <> 'command.denied' order by x.id"},
		{"dispatch", `select count(*)::text from core.jobs j join core.events x on j.payload->>'event_id' = x.id::text
		              where j.kind = 'event.dispatch' and x.actor_type in ('user','agent') and x.kind <> 'command.denied'`},
		{"usage", "select kind || ' ' || quantity || ' ' || meta::text from core.usage order by 1"},
	} {
		rows, err := tx.Query(context.Background(), q.sql)
		if err != nil {
			r.e.t.Fatalf("%s: %v", q.name, err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				r.e.t.Fatal(err)
			}
			out = append(out, q.name+" "+r.norm(s))
		}
		rows.Close()
	}
	return out
}

func TestDifferentialDocs(t *testing.T) {
	e := newEnv(t)
	run := func(base string) *runner {
		r := &runner{e: e, base: base, f: e.fixture(), vars: map[string]string{}, toks: map[string]string{}}
		docsScript(r)
		e.settle(r.f.owner.ws)
		r.transcript = append(r.transcript, r.docsDump()...)
		return r
	}
	rust, gov := run(e.rust), run(e.gosv.URL)
	if len(rust.transcript) != len(gov.transcript) {
		seen := map[string]int{}
		for _, l := range rust.transcript {
			seen[l]++
		}
		for _, l := range gov.transcript {
			seen[l]--
		}
		for l, n := range seen {
			if n != 0 {
				t.Logf("%+d %.600s", n, l)
			}
		}
		t.Fatalf("transcript lengths differ: rust %d go %d", len(rust.transcript), len(gov.transcript))
	}
	bad, ok2xx, errs := 0, 0, 0
	for i := range rust.transcript {
		if rust.transcript[i] != gov.transcript[i] {
			bad++
			if bad <= 15 {
				t.Errorf("step %d differs\n rust %.900s\n go   %.900s", i, rust.transcript[i], gov.transcript[i])
			}
		}
		if strings.Contains(rust.transcript[i], "-> 200 ") {
			ok2xx++
		} else if strings.Contains(rust.transcript[i], " -> 4") {
			errs++
		}
	}
	if ok2xx < 80 || errs < 100 {
		t.Fatalf("script not rich enough: %d ok, %d errors", ok2xx, errs)
	}
	t.Logf("compared %d steps and rows (%d ok responses, %d 4xx); %d differ", len(rust.transcript), ok2xx, errs, bad)

	// reads: raw bytes, Go vs Rust, on both fixtures (same database state)
	for _, r := range []*runner{rust, gov} {
		reads := 0
		for _, cn := range []string{"owner", "member", "bob", "carol", "guest", "agent", "apikey", "other", "noauth", "badkey"} {
			w := r.f.who(cn)
			type call struct {
				cmd string
				in  any
			}
			calls := []call{
				{"doc.flags", map[string]any{}}, {"doc.archived", map[string]any{}}, {"doc.archived", map[string]any{"space_id": r.f.space}},
				{"doc.proposals", map[string]any{}}, {"doc.proposals", map[string]any{"status": "approved"}}, {"doc.proposals", map[string]any{"status": "rejected"}},
				{"doc.proposals", map[string]any{"note_id": r.v("p2")}}, {"doc.proposals", map[string]any{"status": "bogus", "note_id": nil}},
				{"doc.freshness_score", map[string]any{}}, {"doc.freshness_score", map[string]any{"space_id": r.f.space, "window_days": 0}},
				{"doc.freshness_score", map[string]any{"window_days": 1}}, {"doc.freshness_score", map[string]any{"window_days": -5}},
				{"doc.freshness_score", map[string]any{"window_days": 2147483647}}, {"doc.freshness_score", map[string]any{"window_days": 2147483648}},
				{"capture.list", map[string]any{}}, {"capture.list", map[string]any{"status": "answered"}}, {"capture.list", map[string]any{"status": "dismissed"}},
				{"template.list", map[string]any{}}, {"template.list", map[string]any{"source_kind": "deal"}}, {"template.list", map[string]any{"kind": "board"}},
				{"template.list", map[string]any{"source_kind": "contact", "kind": "page"}}, {"template.list", map[string]any{"source_kind": "ünï"}},
				{"template.fields", map[string]any{"source_kind": "invoice"}}, {"template.fields", map[string]any{"source_kind": "item"}},
			}
			for _, c := range calls {
				time.Sleep(55 * time.Millisecond) // stay under the fair-use refill rate of the shared Rust server
				g := e.cmd(e.gosv.URL, w, r.f.owner.ws, c.cmd, c.in, nil)
				ru := e.cmd(e.rust, w, r.f.owner.ws, c.cmd, c.in, nil)
				reads++
				if g.status != ru.status || string(g.body) != string(ru.body) {
					t.Errorf("%s %s %v\n go   %d %.700s\n rust %d %.700s", cn, c.cmd, c.in, g.status, g.body, ru.status, ru.body)
				}
			}
		}
		t.Logf("raw-compared %d read pairs", reads)
	}
}

func (r resp) jsonList() []map[string]any {
	var l []map[string]any
	_ = json.Unmarshal(r.body, &l)
	return l
}
