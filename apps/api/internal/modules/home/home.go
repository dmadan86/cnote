// Package home is the workspace home dashboard: one read command, home.summary, with the tiles and the
// "needs your attention" list. Every query reuses the visibility rules of the list commands it summarizes
// (readable spaces, core.note_access, the Member gate of report.*), and a module switched off in
// settings.modules contributes nothing.
package home

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/jackc/pgx/v5"
)

func Commands() []command.Command {
	return []command.Command{command.NewCommand("home.summary", command.GroupRead, summary)}
}

const attentionMax = 10

// Item is one row of the attention list. Severity: overdue > due > info.
type Item struct {
	Kind        string  `json:"kind"` // task | invoice | bill | quote | deal | confirmation | approval
	ID          string  `json:"id"`
	Key         *string `json:"key"` // task key (ENG-42): the route of an item
	Title       string  `json:"title"`
	Reason      string  `json:"reason"`
	AmountMinor *int64  `json:"amount_minor"`
	Date        *string `json:"date"`
	Severity    string  `json:"severity"`
	DaysOverdue int64   `json:"days_overdue"`
}

var sevRank = map[string]int{"overdue": 3, "due": 2, "info": 1}

func days(n int64) string {
	if n == 1 {
		return "1 day"
	}
	return strconv.FormatInt(n, 10) + " days"
}

// vis is the page-level visibility predicate every query shares ($1 readable spaces, $2 user, $3 admin).
const vis = ` n.deleted_at is null and n.space_id = any($1::text[]::uuid[]) and core.note_access(n.id, $2::uuid, $3, $1::text[]::uuid[]) <> 'none' `

type run struct {
	c     *command.Ctx
	items []Item
}

// add runs a bounded list query and appends the rows the scan func turns into items.
func (r *run) add(sql string, args []any, scan func(pgx.Rows) (Item, error)) error {
	rows, err := r.c.Tx.Query(r.c.Context, sql, args...)
	if err != nil {
		return command.DB(err)
	}
	defer rows.Close()
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return command.DB(err)
		}
		r.items = append(r.items, it)
	}
	return command.DB(rows.Err())
}

func summary(c *command.Ctx, input json.RawMessage) (any, error) {
	var o map[string]json.RawMessage
	if json.Unmarshal(input, &o) != nil || o == nil {
		return nil, command.ErrInvalid("invalid input")
	}
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select settings from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&raw); err != nil {
		return nil, command.DB(err)
	}
	var st struct {
		Modules *[]string `json:"modules"`
	}
	_ = json.Unmarshal(raw, &st)
	on := func(k string) bool { return st.Modules == nil || slices.Contains(*st.Modules, k) }

	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	role := c.Principal.Role
	fin, err := c.FinanceAllowed()
	if err != nil {
		return nil, err
	}
	today := billing.TodayIST().Format("2006-01-02")
	base := []any{spaces, uid, admin, today} // $4 = today (IST)
	tiles := map[string]any{}
	r := &run{c: c}
	var overdueTasks, overdueInvoices int64
	var haveTasks bool

	// ---- tasks: assigned to me, not done, due today or earlier (projects) ----
	if on("projects") && uid != nil {
		const w = ` from projects.items i join core.notes n on n.id = i.note_id
			 where i.assignee_id = $2::uuid and i.status_category <> 'done' and i.due_date <= $4::date and` + vis
		var due int64
		if err := c.Tx.QueryRow(c.Context, `select count(*), count(*) filter (where i.due_date < $4::date)`+w, base...).Scan(&due, &overdueTasks); err != nil {
			return nil, command.DB(err)
		}
		haveTasks = true
		tiles["tasks_due"] = map[string]any{"count": due, "overdue": overdueTasks}
		if err := r.add(`select n.id::text, i.key, n.title, i.due_date::text, ($4::date - i.due_date)`+w+` order by i.due_date, n.created_at limit 10`, base,
			func(rs pgx.Rows) (Item, error) {
				it := Item{Kind: "task", Severity: "due", Reason: "Due today"}
				var key, d string
				err := rs.Scan(&it.ID, &key, &it.Title, &d, &it.DaysOverdue)
				it.Key, it.Date = &key, &d
				if it.DaysOverdue > 0 {
					it.Severity, it.Reason = "overdue", "Overdue by "+days(it.DaysOverdue)
				}
				return it, err
			}); err != nil {
			return nil, err
		}
	}

	// ---- new inquiries: deals created in the last 7 days still in the first stage (crm).
	// Guests see none; members only the deals they own (the personal version), admins every readable one.
	if on("crm") && role >= command.Member {
		const w = ` from crm.deals d join core.notes n on n.id = d.note_id
			 where d.stage = 'lead' and n.created_at >= now() - interval '7 days' and ($3 or d.owner_id = $2::uuid) and` + vis
		var cnt int64
		if err := c.Tx.QueryRow(c.Context, "select count(*)"+w, base[:3]...).Scan(&cnt); err != nil {
			return nil, command.DB(err)
		}
		tiles["new_inquiries"] = map[string]any{"count": cnt}
		// a lead already due for follow-up is listed once, as the follow-up below
		if err := r.add("select n.id::text, n.title, d.amount_minor, n.created_at::date::text"+w+" and (d.next_follow_up is null or d.next_follow_up > $4::date) order by n.created_at desc limit 10", base,
			func(rs pgx.Rows) (Item, error) {
				it := Item{Kind: "deal", Severity: "info", Reason: "New inquiry"}
				var amt int64
				var d string
				err := rs.Scan(&it.ID, &it.Title, &amt, &d)
				it.Date = &d
				if amt > 0 {
					it.AmountMinor = &amt
				}
				return it, err
			}); err != nil {
			return nil, err
		}
	}

	// ---- follow-ups: open deals whose next follow-up date is today or earlier (crm, leads). Same audience as the
	// inquiries above: members see their own deals, admins every readable one, guests none.
	if on("crm") && role >= command.Member {
		const w = ` from crm.deals d join core.notes n on n.id = d.note_id
			 where d.next_follow_up <= $4::date and d.stage not in ('won','lost') and ($3 or d.owner_id = $2::uuid) and` + vis
		var due, late int64
		if err := c.Tx.QueryRow(c.Context, `select count(*), count(*) filter (where d.next_follow_up < $4::date)`+w, base...).Scan(&due, &late); err != nil {
			return nil, command.DB(err)
		}
		tiles["follow_ups"] = map[string]any{"count": due, "overdue": late}
		if err := r.add(`select n.id::text, n.title, coalesce(d.next_step,''), d.next_follow_up::text, ($4::date - d.next_follow_up)`+w+` order by d.next_follow_up, n.created_at limit 10`, base,
			func(rs pgx.Rows) (Item, error) {
				it := Item{Kind: "deal", Severity: "due"}
				var step, d string
				err := rs.Scan(&it.ID, &it.Title, &step, &d, &it.DaysOverdue)
				it.Date = &d
				it.Reason = "Follow up today"
				if it.DaysOverdue > 0 {
					it.Severity, it.Reason = "overdue", "Follow-up overdue by "+days(it.DaysOverdue)
				}
				if step != "" {
					it.Reason += ": " + step
				}
				return it, err
			}); err != nil {
			return nil, err
		}
	}

	// ---- money: billing on and role >= member (the report.* gate) and finance access; guests never see amounts ----
	if on("billing") && fin && role >= command.Member {
		// receivables outstanding, as report.receivables_ageing counts them
		const out = `(i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor)`
		const iw = ` from billing.invoices i join core.notes n on n.id = i.note_id
			 where i.kind = 'invoice' and i.status in ('issued','irn_generated','sent','partly_paid') and ` + out + ` > 0 and`
		var cnt, total, oc, om int64
		if err := c.Tx.QueryRow(c.Context, `select count(*), coalesce(sum(`+out+`),0)::bigint, count(*) filter (where i.due_date < $4::date),
			   coalesce(sum(`+out+`) filter (where i.due_date < $4::date),0)::bigint`+iw+vis, base...).Scan(&cnt, &total, &oc, &om); err != nil {
			return nil, command.DB(err)
		}
		overdueInvoices = oc
		tiles["payments_pending"] = map[string]any{"count": cnt, "total_minor": total, "overdue_count": oc, "overdue_minor": om}
		if err := r.add(`select n.id::text, coalesce(i.number,'Draft'), coalesce((select title from core.notes where id = i.contact_id),''), i.due_date::text, `+out+`, ($4::date - i.due_date)`+
			iw+` i.due_date < $4::date and`+vis+` order by i.due_date, `+out+` desc limit 10`, base,
			func(rs pgx.Rows) (Item, error) {
				it := Item{Kind: "invoice", Severity: "overdue"}
				var num, contact, d string
				var amt int64
				err := rs.Scan(&it.ID, &num, &contact, &d, &amt, &it.DaysOverdue)
				it.Title, it.Date, it.AmountMinor = "Invoice "+num, &d, &amt
				it.Reason = contact + ", overdue by " + days(it.DaysOverdue)
				return it, err
			}); err != nil {
			return nil, err
		}

		// purchase bills due within 7 days or overdue
		const bout = `(b.total_minor - b.paid_minor)`
		const bw = ` from billing.purchase_bills b join core.notes n on n.id = b.note_id
			 where b.status in ('recorded','partly_paid') and ` + bout + ` > 0 and b.due_date <= $4::date + 7 and` + vis
		var bc, bt, boc int64
		if err := c.Tx.QueryRow(c.Context, `select count(*), coalesce(sum(`+bout+`),0)::bigint, count(*) filter (where b.due_date < $4::date)`+bw, base...).Scan(&bc, &bt, &boc); err != nil {
			return nil, command.DB(err)
		}
		tiles["bills_due"] = map[string]any{"count": bc, "total_minor": bt, "overdue_count": boc}
		if err := r.add(`select n.id::text, b.vendor_bill_number, coalesce((select title from core.notes where id = b.vendor_id),''), b.due_date::text, `+bout+`, ($4::date - b.due_date)`+
			bw+` order by b.due_date, `+bout+` desc limit 10`, base,
			func(rs pgx.Rows) (Item, error) {
				it := Item{Kind: "bill", Severity: "due"}
				var num, vendor, d string
				var amt int64
				err := rs.Scan(&it.ID, &num, &vendor, &d, &amt, &it.DaysOverdue)
				it.Title, it.Date, it.AmountMinor = "Bill "+num+" from "+vendor, &d, &amt
				switch {
				case it.DaysOverdue > 0:
					it.Severity, it.Reason = "overdue", "Payable, overdue by "+days(it.DaysOverdue)
				case it.DaysOverdue == 0:
					it.Reason = "Payable today"
				default:
					it.Reason = "Payable in " + days(-it.DaysOverdue)
				}
				return it, err
			}); err != nil {
			return nil, err
		}
	}

	// ---- approvals: pending confirmations + quotes sent and awaiting the customer ----
	var confirmations, quotes, decisions int64
	haveApprovals := false
	if role >= command.Member {
		haveApprovals = true
		// admins see every pending confirmation (confirmation.decide is member-level, but the queue is the owner's);
		// members see the ones they or their agents requested
		const cw = ` from core.confirmations f where f.status = 'pending' and f.kind = 'agent' and ($1 or f.requested_by = $2::text
			 or f.requested_by in (select a.id::text from core.agents a where a.owner_user_id = $2::uuid))`
		if err := c.Tx.QueryRow(c.Context, "select count(*)"+cw, admin, uid).Scan(&confirmations); err != nil {
			return nil, command.DB(err)
		}
		if err := r.add("select f.id::text, f.command, f.created_at::date::text"+cw+" order by f.created_at limit 10", []any{admin, uid},
			func(rs pgx.Rows) (Item, error) {
				it := Item{Kind: "confirmation", Severity: "due"}
				var cmdName, d string
				err := rs.Scan(&it.ID, &cmdName, &d)
				it.Title, it.Date, it.Reason = "Approve "+cmdName, &d, "An agent or API call is waiting for a human to confirm it"
				return it, err
			}); err != nil {
			return nil, err
		}
	}
	// human approval requests this person can decide (WP1): admins every one, others those naming them; never their own unless owner
	if role >= command.Member && uid != nil {
		const dw = ` from core.confirmations f where f.status = 'pending' and f.kind = 'approval' and ($1 or f.approver_user_id = $2::uuid)
			 and (f.requested_by <> $2::text or $3)`
		args := []any{admin, uid, role == command.Owner}
		if err := c.Tx.QueryRow(c.Context, "select count(*)"+dw, args...).Scan(&decisions); err != nil {
			return nil, command.DB(err)
		}
		if err := r.add("select f.id::text, coalesce(n.title, f.command), f.amount_minor, f.created_at::date::text, f.command from core.confirmations f left join core.notes n on n.id = f.note_id"+
			dw[len(" from core.confirmations f"):]+" order by f.created_at limit 10", args,
			func(rs pgx.Rows) (Item, error) {
				it := Item{Kind: "approval", Severity: "due", Reason: "Waiting for your approval"}
				var d, cmdName string
				err := rs.Scan(&it.ID, &it.Title, &it.AmountMinor, &d, &cmdName)
				it.Date = &d
				return it, err
			}); err != nil {
			return nil, err
		}
		haveApprovals = true
	}
	if on("billing") && role >= command.Member {
		const qw = ` from billing.quotations q join core.notes n on n.id = q.note_id where q.status = 'sent' and` + vis
		if err := c.Tx.QueryRow(c.Context, "select count(*)"+qw, base[:3]...).Scan(&quotes); err != nil {
			return nil, command.DB(err)
		}
		haveApprovals = true
		if err := r.add("select n.id::text, coalesce(q.number,'Quote'), coalesce((select title from core.notes where id = q.contact_id),''), q.sent_at::date::text, q.total_minor, ($4::date - q.sent_at::date)"+qw+" order by q.sent_at limit 10", base,
			func(rs pgx.Rows) (Item, error) {
				it := Item{Kind: "quote", Severity: "info"}
				var num, contact, d string
				var amt, age int64
				err := rs.Scan(&it.ID, &num, &contact, &d, &amt, &age)
				it.Title, it.Date, it.AmountMinor = "Quote "+num, &d, &amt
				it.Reason = "Waiting on " + contact + " for " + days(age)
				return it, err
			}); err != nil {
			return nil, err
		}
	}
	if haveApprovals {
		tiles["approvals"] = map[string]any{"count": confirmations + quotes + decisions, "confirmations": confirmations, "quotes_awaiting": quotes, "to_approve": decisions}
	}

	// ---- urgent: overdue items + overdue invoices ----
	if haveTasks || on("billing") && fin && role >= command.Member {
		tiles["urgent"] = map[string]any{"count": overdueTasks + overdueInvoices, "overdue_tasks": overdueTasks, "overdue_invoices": overdueInvoices}
	}

	// severity first, then the longest overdue, then the biggest amount; each source is already capped at 10
	// in that order, so the merged top 10 is exact.
	amount := func(i Item) int64 {
		if i.AmountMinor == nil {
			return 0
		}
		return *i.AmountMinor
	}
	sort.SliceStable(r.items, func(a, b int) bool {
		x, y := r.items[a], r.items[b]
		if sevRank[x.Severity] != sevRank[y.Severity] {
			return sevRank[x.Severity] > sevRank[y.Severity]
		}
		if x.DaysOverdue != y.DaysOverdue {
			return x.DaysOverdue > y.DaysOverdue
		}
		return amount(x) > amount(y)
	})
	if len(r.items) > attentionMax {
		r.items = r.items[:attentionMax]
	}
	if r.items == nil {
		r.items = []Item{}
	}
	view := "owner"
	if role < command.Admin {
		view = "personal"
	}
	return map[string]any{"as_of": today, "view": view, "tiles": tiles, "attention": r.items}, nil
}
