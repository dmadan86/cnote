package server_test

import (
	"context"
	"testing"

	"github.com/cnote/api/internal/modules/approvals"
)

// Approvals are Go-only: fixtures come from Rust, every approval.* and quote.* call runs on the Go server.
func (b *bt) rule(in map[string]any) map[string]any {
	if in["command"] == nil {
		in["command"] = "quote.send"
	}
	return b.ok("approval.rule_set", in)
}

func (b *bt) approvals(w who, in map[string]any) []map[string]any {
	b.t.Helper()
	out := []map[string]any{}
	return append(out, b.call(w, "approval.list", in).jsonList()...)
}

func (b *bt) sendReq(w who, q string) map[string]any {
	b.t.Helper()
	r := b.okAs(w, "quote.send", map[string]any{"id": q})
	eq(b.t, r["approval_required"], true)
	return r
}

func (b *bt) notifs(w who) []string {
	r := b.call(w, "notification.list", map[string]any{})
	out := []string{}
	for _, x := range r.jsonList() {
		out = append(out, x["kind"].(string)+"|"+x["note_title"].(string))
	}
	return out
}

func TestApprovalThresholdHoldsApprovesAndReplaysOnce(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	b.rule(map[string]any{"min_amount_minor": 50_000})
	small := b.okAs(member, "quote.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "x", "unit_price_minor": 49_999}}})["id"].(string)
	eq(t, b.okAs(member, "quote.send", map[string]any{"id": small})["status"], "sent") // below the threshold: straight through

	big := b.quote(b.contact, "")
	r := b.sendReq(member, big)
	id := r["confirmation_id"].(string)
	eq(t, n(r["amount_minor"]), int64(110_008))
	eq(t, b.ok("quote.get", map[string]any{"id": big})["status"], "draft") // not executed
	eq(t, b.sendReq(member, big)["confirmation_id"], id)                   // asking again does not open a second request
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.confirmations where kind = 'approval'"), []string{"1"})

	mine := b.approvals(member, map[string]any{"status": "pending"})
	eq(t, len(mine), 1)
	eq(t, mine[0]["can_decide"], false)
	eq(t, mine[0]["can_cancel"], true)
	ow := b.approvals(b.owner, map[string]any{"status": "pending"})
	eq(t, ow[0]["can_decide"], true)
	eq(t, ow[0]["requester_name"], "Mia")
	eq(t, len(b.notifs(b.owner)) >= 1, true)
	eq(t, b.notifs(b.owner)[0][:18], "approval_requested")

	b.errAs(member, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
	b.errAs(member, "approval.rule_set", map[string]any{"command": "quote.send"}, 403, "forbidden")
	b.errIs("approval.decide", map[string]any{"id": id, "decision": "maybe"}, 400, "invalid")

	d := b.ok("approval.decide", map[string]any{"id": id, "decision": "approve", "reason": "ok"})
	eq(t, d["status"], "approved")
	eq(t, d["result"].(map[string]any)["status"], "sent")
	eq(t, b.ok("quote.get", map[string]any{"id": big})["status"], "sent")
	b.errIs("approval.decide", map[string]any{"id": id, "decision": "approve"}, 409, "conflict") // executes once
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.events where kind = 'quote.sent' and note_id = $1::uuid", big), []string{"1"})
	// the quote was sent as the requester, and both steps are on the page history
	eq(t, b.dbStrings(b.owner.ws, "select actor_id::text from core.events where kind = 'quote.sent' and note_id = $1::uuid", big), []string{member.userID})
	eq(t, count(b.events(big), "approval.requested"), 1)
	eq(t, count(b.events(big), "approval.decided"), 1)
	eq(t, b.notifs(member)[0][:17], "approval_decided|")
	eq(t, b.approvals(member, map[string]any{"status": "approved"})[0]["status"], "approved")

	// the owner is never held
	own := b.quote(b.contact, "")
	eq(t, b.q("quote.send", own)["status"], "sent")
}

func TestApprovalRejectCancelAndSelfApproval(t *testing.T) {
	b := newBT(t, false)
	admin := b.addMember(b.owner, b.signup("Ada", false), "admin")
	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	other := b.addMember(b.owner, b.signup("Otto", false), "member")
	b.rule(map[string]any{"min_amount_minor": 1})

	q := b.quote(b.contact, "")
	id := b.sendReq(member, q)["confirmation_id"].(string)
	b.errAs(b.owner, "approval.decide", map[string]any{"id": id, "decision": "reject"}, 400, "invalid") // reason required
	b.errAs(other, "approval.cancel", map[string]any{"id": id}, 403, "forbidden")
	eq(t, len(b.approvals(other, map[string]any{})), 0) // members only see their own and those that name them
	eq(t, b.okAs(admin, "approval.decide", map[string]any{"id": id, "decision": "reject", "reason": "wrong rate"})["status"], "rejected")
	eq(t, b.ok("quote.get", map[string]any{"id": q})["status"], "draft")
	eq(t, b.approvals(member, map[string]any{})[0]["reason"], "wrong rate")
	eq(t, b.notifs(member)[0][:17], "approval_decided|")
	// the author edits and asks again: a new request
	id2 := b.sendReq(member, q)["confirmation_id"].(string)
	if id2 == id {
		t.Fatal("rejected request reused")
	}
	b.errIs("approval.cancel", map[string]any{"id": id2}, 403, "forbidden") // the owner is not the requester
	eq(t, b.okAs(member, "approval.cancel", map[string]any{"id": id2})["status"], "cancelled")
	b.errAs(member, "approval.cancel", map[string]any{"id": id2}, 409, "conflict")

	// an admin who asks cannot approve their own request; the owner can
	q2 := b.quote(b.contact, "")
	id3 := b.sendReq(admin, q2)["confirmation_id"].(string)
	b.errAs(admin, "approval.decide", map[string]any{"id": id3, "decision": "approve"}, 403, "forbidden")
	eq(t, b.ok("approval.decide", map[string]any{"id": id3, "decision": "approve"})["status"], "approved")
	eq(t, b.ok("quote.get", map[string]any{"id": q2})["status"], "sent")
}

func TestApprovalNamedApproverAndEditedDraftExpires(t *testing.T) {
	b := newBT(t, false)
	asha := b.addMember(b.owner, b.signup("Asha", false), "member")
	mia := b.addMember(b.owner, b.signup("Mia", false), "member")
	otto := b.addMember(b.owner, b.signup("Otto", false), "member")
	b.rule(map[string]any{"min_amount_minor": 1, "approver_user_id": asha.userID})

	q := b.quote(b.contact, "")
	id := b.sendReq(mia, q)["confirmation_id"].(string)
	eq(t, b.notifs(asha)[0][:18], "approval_requested")
	eq(t, len(b.notifs(otto)), 0)
	b.errAs(otto, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
	eq(t, b.approvals(asha, map[string]any{"mine": false})[0]["can_decide"], true)
	eq(t, b.okAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"})["status"], "approved") // approver needs no page access

	// the draft changes after the request: what is approved must be what was asked
	q2 := b.quote(b.contact, "")
	id2 := b.sendReq(mia, q2)["confirmation_id"].(string)
	b.okAs(mia, "quote.update_draft", map[string]any{"id": q2, "lines": []any{map[string]any{"description": "Cheaper", "unit_price_minor": 5_000}}})
	d := b.okAs(asha, "approval.decide", map[string]any{"id": id2, "decision": "approve"})
	eq(t, d["status"], "expired")
	eq(t, b.ok("quote.get", map[string]any{"id": q2})["status"], "draft")
	eq(t, b.approvals(mia, map[string]any{"status": "expired"})[0]["id"], id2)
}

func TestApprovalRuleValidationAndOtherCommands(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	guest := b.addMember(b.owner, b.signup("Gus", false), "guest")
	outsider := b.signup("Eve", false)
	b.errIs("approval.rule_set", map[string]any{"command": "note.create"}, 400, "invalid")
	b.errIs("approval.rule_set", map[string]any{"command": "quote.send", "min_amount_minor": -1}, 400, "invalid")
	b.errIs("approval.rule_set", map[string]any{"command": "quote.send", "approver_role": "member"}, 400, "invalid")
	b.errIs("approval.rule_set", map[string]any{"command": "quote.send", "escalate_after_hours": 24}, 400, "invalid")
	b.errIs("approval.rule_set", map[string]any{"command": "quote.send", "escalate_after_hours": 0, "fallback_user_id": member.userID}, 400, "invalid")
	b.errIs("approval.rule_set", map[string]any{"command": "quote.send", "approver_user_id": outsider.userID}, 400, "invalid")
	b.errAs(guest, "approval.rule_list", map[string]any{}, 403, "forbidden")
	b.errAs(guest, "approval.list", map[string]any{}, 403, "forbidden")
	eq(t, len(b.okAs(member, "approval.rule_list", map[string]any{})["commands"].([]any)), 8)
	r := b.rule(map[string]any{"min_amount_minor": 7, "fallback_user_id": member.userID, "escalate_after_hours": 24})
	eq(t, r["approver_role"], "admin")
	r2 := b.rule(map[string]any{"min_amount_minor": 9, "enabled": false}) // one rule per command: updated in place
	eq(t, r2["id"], r["id"])
	eq(t, len(b.ok("approval.rule_list", map[string]any{})["rules"].([]any)), 1)
	q := b.quote(b.contact, "")
	eq(t, b.okAs(member, "quote.send", map[string]any{"id": q})["status"], "sent") // disabled rule holds nothing

	// bill.pay: the rule looks at the bill's total, so paying a large bill in small parts can't slip under it;
	// the replay pays exactly the amount asked for
	b.rule(map[string]any{"command": "bill.pay", "min_amount_minor": 20_000})
	bill := b.bill(b.vendor("29", "Vendor"), "V-1")
	b.okAs(member, "bill.record", map[string]any{"id": bill})
	if total := n(b.bq("bill.get", bill)["total_minor"]); total < 40_000 {
		t.Fatalf("fixture bill total %d too small for this test", total)
	}
	small := b.okAs(member, "bill.pay", map[string]any{"id": bill, "amount_minor": 19_999})
	eq(t, small["approval_required"], true)
	b.ok("approval.decide", map[string]any{"id": small["confirmation_id"], "decision": "approve"})
	eq(t, n(b.bq("bill.get", bill)["paid_minor"]), int64(19_999))
	// bill.pay is also a Confirm command: a human member is held by the rule, not by the agent gate
	h := b.okAs(member, "bill.pay", map[string]any{"id": bill, "amount_minor": 20_000})
	eq(t, h["approval_required"], true)
	b.ok("approval.decide", map[string]any{"id": h["confirmation_id"], "decision": "approve"})
	eq(t, n(b.bq("bill.get", bill)["paid_minor"]), int64(39_999))
	// access is checked before a request is stored: a stranger gets not_found, not a pending request
	b.errAs(outsider, "bill.pay", map[string]any{"id": bill, "amount_minor": 50_000}, 403, "forbidden")
	// a guest cannot even reach the gate
	b.errAs(guest, "quote.send", map[string]any{"id": q}, 403, "forbidden")
}

func TestApprovalAgentsAndConfirmationsStayApart(t *testing.T) {
	b := newBT(t, false)
	b.rule(map[string]any{"min_amount_minor": 1})
	b.must(b.owner, "workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "manage_crm", "manage_invoices", "admin"}}})
	key := b.apiKey(b.owner)
	b.errAs(key, "approval.rule_set", map[string]any{"command": "quote.send"}, 403, "forbidden")
	q := b.quote(b.contact, "")
	// a machine at or above the threshold is refused: it can't open an approval request, and its own confirmation
	// could be decided by the member who owns it
	b.errAs(key, "quote.send", map[string]any{"id": q}, 403, "forbidden")
	eq(t, b.ok("quote.get", map[string]any{"id": q})["status"], "draft")

	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	q2 := b.quote(b.contact, "")
	id := b.sendReq(member, q2)["confirmation_id"].(string)
	b.errAs(key, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
	// the two queues do not mix
	b.errIs("confirmation.decide", map[string]any{"id": id, "approve": true}, 404, "not_found")
	for _, c := range b.okList("confirmation.list", map[string]any{}) {
		if c.(map[string]any)["id"] == id {
			t.Fatal("approval listed as an agent confirmation")
		}
	}
}

func TestApprovalTenantIsolationAndIdempotentRequest(t *testing.T) {
	b := newBT(t, false)
	member := b.addMember(b.owner, b.signup("Mia", false), "member")
	b.rule(map[string]any{"min_amount_minor": 1})
	q := b.quote(b.contact, "")
	hdr := map[string]string{"Idempotency-Key": rnd()}
	first := b.cmd(b.gosv.URL, member, b.owner.ws, "quote.send", map[string]any{"id": q}, hdr)
	second := b.cmd(b.gosv.URL, member, b.owner.ws, "quote.send", map[string]any{"id": q}, hdr)
	eq(t, first.json()["confirmation_id"], second.json()["confirmation_id"])
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.confirmations where kind = 'approval'"), []string{"1"})
	id := first.json()["confirmation_id"].(string)

	o := b.env.tenant(false)
	eq(t, len(o.okList("approval.list", map[string]any{})), 0)
	o.errIs("approval.decide", map[string]any{"id": id, "decision": "approve"}, 404, "not_found")
	eq(t, len(o.ok("approval.rule_list", map[string]any{})["rules"].([]any)), 0)
	if r := o.cmd(o.gosv.URL, o.owner, b.owner.ws, "approval.list", map[string]any{}, nil); r.status == 200 {
		t.Fatalf("foreign workspace path: %s", r.body)
	}
}

func TestApprovalSweepEscalatesAndRemindsOncePerDay(t *testing.T) {
	b := newBT(t, false)
	fallback := b.addMember(b.owner, b.signup("Fay", false), "member")
	asha := b.addMember(b.owner, b.signup("Asha", false), "member")
	mia := b.addMember(b.owner, b.signup("Mia", false), "member")
	b.rule(map[string]any{"min_amount_minor": 1, "approver_user_id": asha.userID, "fallback_user_id": fallback.userID, "escalate_after_hours": 24})
	q := b.quote(b.contact, "")
	id := b.sendReq(mia, q)["confirmation_id"].(string)

	sweep := func() {
		t.Helper()
		if err := approvals.Sweep(context.Background(), b.pool, todayIST()); err != nil {
			t.Fatal(err)
		}
	}
	sweep() // not due yet
	eq(t, len(b.notifs(fallback)), 0)
	if err := b.raw("update core.confirmations set escalate_at = now() - interval '1 minute', created_at = now() - interval '25 hours' where id = '" + id + "'"); err != nil {
		t.Fatal(err)
	}
	sweep()
	sweep() // a second run the same day changes nothing
	eq(t, b.notifs(fallback)[0][:18], "approval_requested")
	eq(t, count(b.notifs(fallback), "approval_requested|"+b.ok("note.get", map[string]any{"id": q})["title"].(string)), 1)
	eq(t, b.dbStrings(b.owner.ws, "select approver_user_id::text from core.confirmations where id = $1::uuid", id), []string{fallback.userID})
	eq(t, count(b.events(q), "approval.escalated"), 1)
	eq(t, b.okAs(fallback, "approval.decide", map[string]any{"id": id, "decision": "approve"})["status"], "approved")
	b.errAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 409, "conflict")

	// reminders: the pending request older than a day reaches its approver once per day
	q2 := b.quote(b.contact, "")
	id2 := b.sendReq(mia, q2)["confirmation_id"].(string)
	if err := b.raw("update core.confirmations set created_at = now() - interval '30 hours' where id = '" + id2 + "'"); err != nil {
		t.Fatal(err)
	}
	sweep()
	sweep()
	rem := 0
	for _, k := range b.notifs(asha) {
		if k[:8] == "reminder" {
			rem++
		}
	}
	eq(t, rem, 1)
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.notifications where kind = 'reminder' and source = 'approvals'"), []string{"2"})
}

func TestApprovalHomeTileCountsDecidableRequests(t *testing.T) {
	b := newBT(t, false)
	mia := b.addMember(b.owner, b.signup("Mia", false), "member")
	b.rule(map[string]any{"min_amount_minor": 1})
	q := b.quote(b.contact, "")
	id := b.sendReq(mia, q)["confirmation_id"].(string)
	eq(t, tile(t, b.home(b.owner), "approvals")["to_approve"], float64(1))
	eq(t, tile(t, b.home(mia), "approvals")["to_approve"], float64(0)) // never their own
	eq(t, tile(t, b.home(b.owner), "approvals")["count"], float64(1))
	if k := kinds(b.home(b.owner)); len(k) == 0 || k[0] != "approval" {
		t.Fatalf("attention %v", k)
	}
	b.ok("approval.decide", map[string]any{"id": id, "decision": "reject", "reason": "no"})
	eq(t, tile(t, b.home(b.owner), "approvals")["to_approve"], float64(0))
}
