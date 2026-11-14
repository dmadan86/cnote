package server_test

import (
	"sync"
	"testing"

	"github.com/cnote/api/internal/modules/billing"
)

// ---------- approval chains ----------

func user(id string) map[string]any { return map[string]any{"kind": "user", "user_id": id} }
func role(r string) map[string]any  { return map[string]any{"kind": "role", "role": r} }
func step(mode string, min int, a ...map[string]any) map[string]any {
	return map[string]any{"mode": mode, "min_amount_minor": min, "approvers": a}
}

func (b *bt) chain(steps ...map[string]any) {
	b.rule(map[string]any{"min_amount_minor": 1, "steps": steps})
}

func (b *bt) decide(w who, id, d string) resp {
	return b.call(w, "approval.decide", map[string]any{"id": id, "decision": d, "reason": "r"})
}

func TestApprovalChainAdvancesStepByStepAndExecutesOnce(t *testing.T) {
	b := newBT(t, false)
	asha := b.memberAs("Asha", "member")
	otto := b.memberAs("Otto", "member")
	ada := b.memberAs("Ada", "admin")
	mia := b.memberAs("Mia", "member")
	b.chain(step("any", 0, user(asha.userID)), step("any", 0, role("admin")))

	q := b.quote(b.contact, "")
	id := b.sendReq(mia, q)["confirmation_id"].(string)
	eq(t, len(b.notifs(ada)), 0) // only step one is told
	eq(t, b.notifs(asha)[0][:18], "approval_requested")
	b.errAs(otto, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
	b.errAs(mia, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")

	r := b.okAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"})
	eq(t, r["status"], "pending")
	eq(t, n(r["step"]), int64(1))
	eq(t, b.ok("quote.get", map[string]any{"id": q})["status"], "draft") // not executed yet
	eq(t, b.notifs(ada)[0][:18], "approval_requested")                   // step two is told
	b.errAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
	row := b.approvals(ada, map[string]any{"status": "pending"})[0]
	eq(t, row["can_decide"], true)
	eq(t, n(row["step"]), int64(1))
	eq(t, len(row["decisions"].([]any)), 1)
	eq(t, b.approvals(asha, map[string]any{"status": "pending"})[0]["can_decide"], false) // sees it, no longer theirs

	r = b.okAs(ada, "approval.decide", map[string]any{"id": id, "decision": "approve"})
	eq(t, r["status"], "approved")
	eq(t, b.ok("quote.get", map[string]any{"id": q})["status"], "sent")
	b.errAs(ada, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 409, "conflict")
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.events where kind = 'quote.sent' and note_id = $1::uuid", q), []string{"1"})
	eq(t, count(b.events(q), "approval.step_approved"), 1)
}

func TestApprovalChainRejectAtAnyStepEndsIt(t *testing.T) {
	b := newBT(t, false)
	asha := b.memberAs("Asha", "member")
	mia := b.memberAs("Mia", "member")
	b.chain(step("any", 0, user(asha.userID)), step("any", 0, role("owner")))
	q := b.quote(b.contact, "")
	id := b.sendReq(mia, q)["confirmation_id"].(string)
	b.okAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"})
	b.errIs("approval.decide", map[string]any{"id": id, "decision": "reject"}, 400, "invalid") // reason needed
	eq(t, b.decide(b.owner, id, "reject").json()["status"], "rejected")                        // owner rejects at step two
	eq(t, b.ok("quote.get", map[string]any{"id": q})["status"], "draft")
	b.errAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 409, "conflict")
	eq(t, b.approvals(mia, map[string]any{"status": "rejected"})[0]["id"], id)
	// a rejection at step one ends it as well
	q2 := b.quote(b.contact, "")
	id2 := b.sendReq(mia, q2)["confirmation_id"].(string)
	eq(t, b.decide(asha, id2, "reject").json()["status"], "rejected")
}

func TestApprovalChainRequesterNeverApprovesOwnStep(t *testing.T) {
	b := newBT(t, false)
	ada := b.memberAs("Ada", "admin")
	bea := b.memberAs("Bea", "admin")
	asha := b.memberAs("Asha", "member")
	b.chain(step("any", 0, user(asha.userID)), step("any", 0, role("admin")))

	// Asha asks for something she would approve herself: her step goes to the admins, not to her
	q := b.quote(b.contact, "")
	id := b.sendReq(asha, q)["confirmation_id"].(string)
	b.errAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
	eq(t, b.approvals(asha, map[string]any{})[0]["can_decide"], false)
	b.okAs(ada, "approval.decide", map[string]any{"id": id, "decision": "approve"})
	// an admin who asks cannot approve the admin step of their own request
	q2 := b.quote(b.contact, "")
	id2 := b.sendReq(ada, q2)["confirmation_id"].(string)
	b.okAs(asha, "approval.decide", map[string]any{"id": id2, "decision": "approve"})
	b.errAs(ada, "approval.decide", map[string]any{"id": id2, "decision": "approve"}, 403, "forbidden")
	eq(t, b.okAs(bea, "approval.decide", map[string]any{"id": id2, "decision": "approve"})["status"], "approved")
	// the same person never fills a step twice
	b.chain(step("all", 0, role("admin"), role("admin")))
	q3 := b.quote(b.contact, "")
	id3 := b.sendReq(asha, q3)["confirmation_id"].(string)
	b.okAs(ada, "approval.decide", map[string]any{"id": id3, "decision": "approve"})
	b.errAs(ada, "approval.decide", map[string]any{"id": id3, "decision": "approve"}, 403, "forbidden")
	eq(t, b.okAs(bea, "approval.decide", map[string]any{"id": id3, "decision": "approve"})["status"], "approved")
}

func TestApprovalChainThresholdSkipsSteps(t *testing.T) {
	b := newBT(t, false)
	asha := b.memberAs("Asha", "member")
	mia := b.memberAs("Mia", "member")
	// the quote is 110,008 paise: step two (from 1,000,000) does not apply to it
	b.chain(step("any", 0, user(asha.userID)), step("any", 1_000_000, role("owner")))
	q := b.quote(b.contact, "")
	id := b.sendReq(mia, q)["confirmation_id"].(string)
	eq(t, b.okAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"})["status"], "approved")
	eq(t, b.ok("quote.get", map[string]any{"id": q})["status"], "sent")
	// a large quote needs both
	big := b.okAs(mia, "quote.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact,
		"lines": []any{map[string]any{"description": "x", "unit_price_minor": 2_000_000}}})["id"].(string)
	id2 := b.sendReq(mia, big)["confirmation_id"].(string)
	eq(t, b.okAs(asha, "approval.decide", map[string]any{"id": id2, "decision": "approve"})["status"], "pending")
	eq(t, b.decide(b.owner, id2, "approve").json()["status"], "approved")
	// when every step sits above the amount nothing is held
	b.chain(step("any", 10_000_000, role("owner")))
	q3 := b.quote(b.contact, "")
	eq(t, b.okAs(mia, "quote.send", map[string]any{"id": q3})["status"], "sent")
}

func TestApprovalChainAllOfAndDepartmentHead(t *testing.T) {
	b := newBT(t, false)
	asha := b.memberAs("Asha", "member")
	otto := b.memberAs("Otto", "member")
	hana := b.memberAs("Hana", "member")
	mia := b.memberAs("Mia", "member")
	zed := b.memberAs("Zed", "member")
	b.chain(step("all", 0, user(asha.userID), user(otto.userID)))
	q := b.quote(b.contact, "")
	id := b.sendReq(mia, q)["confirmation_id"].(string)
	r := b.okAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"})
	eq(t, r["status"], "pending")
	eq(t, n(r["approvals"]), int64(1))
	b.errAs(asha, "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
	eq(t, b.okAs(otto, "approval.decide", map[string]any{"id": id, "decision": "approve"})["status"], "approved")

	dep := b.okAs(b.owner, "department.create", map[string]any{"name": "Sales", "head_user_id": hana.userID})["id"].(string)
	b.okAs(b.owner, "member.update", map[string]any{"user_id": mia.userID, "department_id": dep})
	b.chain(map[string]any{"approvers": []any{map[string]any{"kind": "department_head"}}})
	q2 := b.quote(b.contact, "")
	id2 := b.sendReq(mia, q2)["confirmation_id"].(string)
	b.errAs(otto, "approval.decide", map[string]any{"id": id2, "decision": "approve"}, 403, "forbidden")
	eq(t, b.okAs(hana, "approval.decide", map[string]any{"id": id2, "decision": "approve"})["status"], "approved")
	// someone with no department head goes to the admins
	q3 := b.quote(b.contact, "")
	id3 := b.sendReq(zed, q3)["confirmation_id"].(string)
	b.errAs(hana, "approval.decide", map[string]any{"id": id3, "decision": "approve"}, 403, "forbidden")
	eq(t, b.decide(b.owner, id3, "approve").json()["status"], "approved")
}

// Two people decide the same step at once: the request row is locked, so each decision lands once and the
// quote is sent exactly once.
func TestApprovalChainConcurrentApproveOnSameStep(t *testing.T) {
	race := func(b *bt, id string, ws ...who) (codes []int) {
		var wg sync.WaitGroup
		out := make([]int, len(ws))
		for i, w := range ws {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = b.decide(w, id, "approve").status
			}()
		}
		wg.Wait()
		return out
	}
	b := newBT(t, false)
	asha := b.memberAs("Asha", "member")
	otto := b.memberAs("Otto", "member")
	mia := b.memberAs("Mia", "member")

	// all-of: both decisions count, one of them completes the request
	b.chain(step("all", 0, user(asha.userID), user(otto.userID)))
	q := b.quote(b.contact, "")
	id := b.sendReq(mia, q)["confirmation_id"].(string)
	codes := race(b, id, asha, otto)
	eq(t, codes, []int{200, 200})
	eq(t, b.ok("quote.get", map[string]any{"id": q})["status"], "sent")
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.approval_decisions where confirmation_id = $1::uuid", id), []string{"2"})
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.events where kind = 'quote.sent' and note_id = $1::uuid", q), []string{"1"})

	// any-of with a second step: one wins the first step, the other is no longer an approver of this request
	b.chain(step("any", 0, user(asha.userID), user(otto.userID)), step("any", 0, role("owner")))
	q2 := b.quote(b.contact, "")
	id2 := b.sendReq(mia, q2)["confirmation_id"].(string)
	codes = race(b, id2, asha, otto)
	if !(codes[0] == 200 && codes[1] == 403 || codes[0] == 403 && codes[1] == 200) {
		t.Fatalf("want one 200 and one 403, got %v", codes)
	}
	eq(t, b.dbStrings(b.owner.ws, "select step_idx::text from core.confirmations where id = $1::uuid", id2), []string{"1"})
	eq(t, b.dbStrings(b.owner.ws, "select count(*)::text from core.approval_decisions where confirmation_id = $1::uuid", id2), []string{"1"})
	eq(t, b.ok("quote.get", map[string]any{"id": q2})["status"], "draft")
}

func TestApprovalChainRuleValidationAndLegacyShape(t *testing.T) {
	b := newBT(t, false)
	asha := b.memberAs("Asha", "member")
	bad := func(steps any) {
		b.errIs("approval.rule_set", map[string]any{"command": "quote.send", "steps": steps}, 400, "invalid")
	}
	bad([]any{})
	bad("nope")
	bad([]any{step("sometimes", 0, role("admin"))})
	bad([]any{step("any", -1, role("admin"))})
	bad([]any{step("any", 0)})
	bad([]any{step("any", 0, map[string]any{"kind": "robot"})})
	bad([]any{step("any", 0, role("member"))})
	bad([]any{step("any", 0, user("00000000-0000-4000-8000-000000000000"))})
	bad([]any{step("any", 0, role("admin")), step("any", 0, role("admin")), step("any", 0, role("admin")), step("any", 0, role("admin")), step("any", 0, role("admin")), step("any", 0, role("admin"))})
	b.errAs(asha, "approval.rule_set", map[string]any{"command": "quote.send", "steps": []any{step("any", 0, role("admin"))}}, 403, "forbidden")
	b.errAs(b.env.apiKey(b.owner), "approval.rule_set", map[string]any{"command": "quote.send", "steps": []any{step("any", 0, role("admin"))}}, 403, "forbidden")

	// the old single-approver shape is stored as a one-step chain
	r := b.rule(map[string]any{"min_amount_minor": 5, "approver_user_id": asha.userID})
	st := r["steps"].([]any)
	eq(t, len(st), 1)
	eq(t, st[0].(map[string]any)["approvers"].([]any)[0].(map[string]any)["user_id"], asha.userID)
	r = b.rule(map[string]any{"min_amount_minor": 5})
	eq(t, r["steps"].([]any)[0].(map[string]any)["approvers"].([]any)[0].(map[string]any)["role"], "admin")
	// a chain given in rule_set comes back from rule_list
	b.chain(step("any", 0, user(asha.userID)), step("all", 100, role("admin"), role("owner")))
	rules := b.ok("approval.rule_list", map[string]any{})["rules"].([]any)
	eq(t, len(rules[0].(map[string]any)["steps"].([]any)), 2)
	// agents and keys cannot decide
	mia := b.memberAs("Mia", "member")
	id := b.sendReq(mia, b.quote(b.contact, ""))["confirmation_id"].(string)
	b.errAs(b.env.apiKey(b.owner), "approval.decide", map[string]any{"id": id, "decision": "approve"}, 403, "forbidden")
}

// ---------- branches ----------

func TestBranchesCrudPermissionsAndMembers(t *testing.T) {
	b := newBT(t, false)
	ada := b.memberAs("Ada", "admin")
	mia := b.memberAs("Mia", "member")
	g := billing.CompleteGSTIN("27ABCDE1234F1Z")
	br := b.okAs(ada, "branch.create", map[string]any{"name": " Pune ", "address": "FC Road", "gstin": g})
	eq(t, br["name"], "Pune")
	eq(t, br["state_code"], "27")
	id := br["id"].(string)
	b.errAs(ada, "branch.create", map[string]any{"name": "Pune"}, 409, "conflict")
	b.errAs(ada, "branch.create", map[string]any{"name": ""}, 400, "invalid")
	b.errAs(ada, "branch.create", map[string]any{"name": "Bad", "gstin": "123"}, 400, "invalid")
	b.errAs(mia, "branch.create", map[string]any{"name": "Nope"}, 403, "forbidden")
	b.errAs(b.env.apiKey(ada), "branch.create", map[string]any{"name": "Nope"}, 403, "forbidden")
	b.errAs(b.memberAs("Gus", "guest"), "branch.list", map[string]any{}, 403, "forbidden")
	eq(t, len(b.list(mia, "branch.list", map[string]any{})), 1)
	b.errAs(mia, "branch.update", map[string]any{"id": id, "name": "X"}, 403, "forbidden")
	b.errAs(ada, "branch.update", map[string]any{"id": id}, 400, "invalid")
	b.errAs(ada, "branch.update", map[string]any{"id": "00000000-0000-4000-8000-000000000000", "name": "Z"}, 404, "not_found")
	up := b.okAs(ada, "branch.update", map[string]any{"id": id, "address": ""})
	eq(t, up["address"], nil)
	eq(t, up["gstin"], g)
	up = b.okAs(ada, "branch.update", map[string]any{"id": id, "gstin": ""})
	eq(t, up["state_code"], nil)
	b.okAs(ada, "branch.update", map[string]any{"id": id, "gstin": g})

	// members belong to one branch; another workspace's branch is not found
	other := b.env.tenant(false)
	ob := other.ok("branch.create", map[string]any{"name": "Elsewhere"})["id"].(string)
	b.errAs(ada, "member.update", map[string]any{"user_id": mia.userID, "branch_id": ob}, 400, "invalid")
	eq(t, b.okAs(ada, "member.update", map[string]any{"user_id": mia.userID, "branch_id": id})["branch"], "Pune")
	eq(t, b.list(mia, "branch.list", map[string]any{})[0].(map[string]any)["members"], float64(1))
	b.errAs(mia, "branch.delete", map[string]any{"id": id}, 403, "forbidden")
	key := b.env.apiKey(b.owner)
	b.ok("workspace.update", map[string]any{"settings": map[string]any{"agent_tool_groups": []string{"read", "destructive"}}})
	eq(t, b.call(key, "branch.delete", map[string]any{"id": id}).json()["confirmation_required"], true)
	b.okAs(ada, "branch.delete", map[string]any{"id": id})
	eq(t, b.people()[mia.userID]["branch_id"], nil)
	b.errAs(ada, "branch.delete", map[string]any{"id": id}, 404, "not_found")
}

func TestInvoiceFromBranchUsesBranchGSTINAndState(t *testing.T) {
	b := newBT(t, false) // workspace GSTIN is Karnataka (29); the buyer is in 29
	g := billing.CompleteGSTIN("27ABCDE1234F1Z")
	pune := b.ok("branch.create", map[string]any{"name": "Pune", "gstin": g})["id"].(string)
	plain := b.ok("branch.create", map[string]any{"name": "Goa office"})["id"].(string)
	lines := []any{map[string]any{"description": "Work", "hsn_sac": "998314", "unit_price_minor": 100_000}}
	mk := func(in map[string]any) map[string]any {
		in["space_id"], in["contact_id"], in["lines"] = b.space, b.contact, lines
		return b.ok("invoice.create_draft", in)
	}
	head := b.ok("invoice.issue", map[string]any{"id": mk(map[string]any{})["id"]})
	eq(t, head["supplier_gstin"], billing.CompleteGSTIN("29ABCDE1234F1Z"))
	eq(t, n(head["igst_minor"]), int64(0))

	d := mk(map[string]any{"branch_id": pune})
	eq(t, d["branch_id"], pune)
	pv := b.ok("invoice.preview", map[string]any{"id": d["id"]})
	eq(t, pv["supplier_state"], "27")
	inv := b.ok("invoice.issue", map[string]any{"id": d["id"]})
	eq(t, inv["supplier_gstin"], g)
	eq(t, inv["supplier_state"], "27")
	if n(inv["igst_minor"]) == 0 || n(inv["cgst_minor"]) != 0 { // Maharashtra seller, Karnataka buyer: inter-state
		t.Fatalf("branch state must drive place of supply: %v", inv)
	}
	// a branch without a GSTIN raises invoices under the workspace GSTIN
	np := b.ok("invoice.issue", map[string]any{"id": mk(map[string]any{"branch_id": plain})["id"]})
	eq(t, np["supplier_gstin"], head["supplier_gstin"])
	eq(t, n(np["igst_minor"]), int64(0))
	b.errIs("invoice.create_draft", map[string]any{"space_id": b.space, "contact_id": b.contact, "lines": lines,
		"branch_id": "00000000-0000-4000-8000-000000000000"}, 400, "invalid")
	// a draft can move branch; a person's branch is the default
	d2 := mk(map[string]any{})
	eq(t, d2["branch_id"], nil)
	eq(t, b.ok("invoice.update_draft", map[string]any{"id": d2["id"], "branch_id": pune})["branch_id"], pune)
	b.ok("member.update", map[string]any{"user_id": b.owner.userID, "branch_id": plain})
	eq(t, mk(map[string]any{})["branch_id"], plain)

	// filters: invoices and the home tiles
	count := func(rows []any) int { return len(rows) }
	eq(t, count(b.okList("invoice.list", map[string]any{"branch_id": pune})), 2)  // the issued one + the draft moved here
	eq(t, count(b.okList("invoice.list", map[string]any{"branch_id": plain})), 2) // issued from plain + the default-branch draft
	eq(t, n(tile(t, b.ok("home.summary", map[string]any{"branch_id": pune}), "payments_pending")["count"]), int64(1))
	eq(t, n(tile(t, b.ok("home.summary", map[string]any{}), "payments_pending")["count"]), int64(3))
	b.errIs("home.summary", map[string]any{"branch_id": "nope"}, 400, "invalid")
	// a branch that raised an issued invoice cannot be deleted; an unused one can, and drafts keep existing
	b.errIs("branch.delete", map[string]any{"id": pune}, 409, "conflict")
	tmp := b.ok("branch.create", map[string]any{"name": "Temp"})["id"].(string)
	dt := mk(map[string]any{"branch_id": tmp})["id"]
	b.ok("branch.delete", map[string]any{"id": tmp})
	eq(t, b.ok("invoice.get", map[string]any{"id": dt})["branch_id"], nil)
}

func TestExpensesCarryBranchAndFilter(t *testing.T) {
	b := newBT(t, false)
	pune := b.ok("branch.create", map[string]any{"name": "Pune"})["id"].(string)
	goa := b.ok("branch.create", map[string]any{"name": "Goa"})["id"].(string)
	mia := b.memberAs("Mia", "member")
	b.ok("member.update", map[string]any{"user_id": mia.userID, "branch_id": goa})
	mk := func(w who, in map[string]any) map[string]any {
		in["spent_on"], in["category"], in["amount_minor"] = day(-1), "travel", 500
		return b.okAs(w, "expense.create", in)
	}
	eq(t, mk(b.owner, map[string]any{"branch_id": pune})["branch_id"], pune)
	eq(t, mk(mia, map[string]any{})["branch_id"], goa) // the claimant's branch
	eq(t, mk(b.owner, map[string]any{})["branch_id"], nil)
	b.errIs("expense.create", map[string]any{"spent_on": day(-1), "category": "travel", "amount_minor": 5, "branch_id": "00000000-0000-4000-8000-000000000000"}, 400, "invalid")
	eq(t, len(b.okList("expense.list", map[string]any{"branch_id": pune})), 1)
	eq(t, len(b.okList("expense.list", map[string]any{"branch_id": goa})), 1)
	eq(t, len(b.okList("expense.list", map[string]any{})), 3)
}
