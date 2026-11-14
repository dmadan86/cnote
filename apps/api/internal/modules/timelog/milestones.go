package timelog

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// A billing milestone is a projects.versions row. bill_amount_minor is the taxable value (before GST) and is always
// stored: a percent is converted against the accepted quotation's subtotal when it is set, and the milestone that
// completes 100% takes the remainder, so no paisa is left unbilled.

type version struct {
	id, space, name string
	quote, invoice  *string
	amount          *int64
	bp              *int
	releaseDate     *string
}

func lockVersion(c *command.Ctx, id string) (version, error) {
	v := version{id: id}
	err := c.Tx.QueryRow(c.Context,
		`select space_id::text, name, quotation_id::text, invoice_id::text, bill_amount_minor, bill_percent_bp, release_date::text
		 from projects.versions where id = $1::uuid for update`, id).Scan(&v.space, &v.name, &v.quote, &v.invoice, &v.amount, &v.bp, &v.releaseDate)
	if err == pgx.ErrNoRows {
		return v, command.ErrNotFound("version")
	}
	if err != nil {
		return v, command.DB(err)
	}
	return v, access.AssertSpaceAccess(c, v.space, true)
}

// invoiced: the milestone already has a live (not cancelled) invoice.
func invoiced(c *command.Ctx, v version) (bool, error) {
	if v.invoice == nil {
		return false, nil
	}
	var st string
	if err := c.Tx.QueryRow(c.Context, "select status from billing.invoices where note_id = $1::uuid", *v.invoice).Scan(&st); err != nil {
		return false, command.DB(err)
	}
	return st != "cancelled", nil
}

func setBilling(c *command.Ctx, raw json.RawMessage) (any, error) {
	in, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err := need("version_id", in.VersionID); err != nil {
		return nil, err
	}
	if (in.BillAmount == nil) == (in.BillPercentBP == nil) {
		return nil, command.ErrInvalid("send exactly one of bill_amount_minor or bill_percent_bp")
	}
	if in.BillAmount != nil && *in.BillAmount <= 0 || in.BillPercentBP != nil && (*in.BillPercentBP < 1 || *in.BillPercentBP > 10000) {
		return nil, command.ErrInvalid("the milestone amount must be positive and the percent between 1 and 10000 basis points")
	}
	v, err := lockVersion(c, in.VersionID)
	if err != nil {
		return nil, err
	}
	if done, err := invoiced(c, v); err != nil || done {
		if err != nil {
			return nil, err
		}
		return nil, command.ErrConflict("this milestone is already invoiced")
	}
	quote := v.quote
	if in.QuotationID != nil {
		quote = in.QuotationID
	}
	if quote == nil && in.BillPercentBP != nil {
		return nil, command.ErrInvalid("a percent needs a quotation_id to take the percent of")
	}
	amount := int64(0)
	if in.BillAmount != nil {
		amount = *in.BillAmount
	}
	if quote != nil {
		var qspace, status string
		var subtotal int64
		err := c.Tx.QueryRow(c.Context,
			`select n.space_id::text, q.status, q.subtotal_minor from billing.quotations q join core.notes n on n.id = q.note_id
			 where q.note_id = $1::uuid for update of q`, *quote).Scan(&qspace, &status, &subtotal)
		if err == pgx.ErrNoRows {
			return nil, command.ErrNotFound("quotation")
		}
		if err != nil {
			return nil, command.DB(err)
		}
		if qspace != v.space || (status != "accepted" && status != "converted") {
			return nil, command.ErrInvalid("the quotation must be accepted and in the same project")
		}
		var sumAmt int64
		var sumBP int
		if err := c.Tx.QueryRow(c.Context,
			"select coalesce(sum(bill_amount_minor),0), coalesce(sum(bill_percent_bp),0) from projects.versions where quotation_id = $1::uuid and id <> $2::uuid",
			*quote, v.id).Scan(&sumAmt, &sumBP); err != nil {
			return nil, command.DB(err)
		}
		if in.BillPercentBP != nil {
			if sumBP+*in.BillPercentBP > 10000 {
				return nil, command.ErrInvalid(fmt.Sprintf("milestone percents would add up to more than 100%% (%d bp already planned)", sumBP))
			}
			amount = (subtotal*int64(*in.BillPercentBP) + 5000) / 10000
			if sumBP+*in.BillPercentBP == 10000 {
				amount = subtotal - sumAmt
			}
		}
		if amount <= 0 || sumAmt+amount > subtotal {
			return nil, command.ErrInvalid(fmt.Sprintf("milestones would bill more than the quotation (%d of %d before GST)", sumAmt+amount, subtotal))
		}
	}
	if _, err := c.Tx.Exec(c.Context,
		"update projects.versions set bill_amount_minor = $2, bill_percent_bp = $3, quotation_id = $4::uuid, invoice_id = null where id = $1::uuid",
		v.id, amount, in.BillPercentBP, quote); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("milestone.billing_set", nil, map[string]any{"version_id": v.id, "space_id": v.space, "amount_minor": amount, "percent_bp": in.BillPercentBP}); err != nil {
		return nil, err
	}
	return map[string]any{"version_id": v.id, "bill_amount_minor": amount, "bill_percent_bp": in.BillPercentBP, "quotation_id": quote}, nil
}

func milestoneInvoice(c *command.Ctx, raw json.RawMessage) (any, error) {
	in, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err := need("version_id", in.VersionID); err != nil {
		return nil, err
	}
	v, err := lockVersion(c, in.VersionID)
	if err != nil {
		return nil, err
	}
	if v.amount == nil {
		return nil, command.ErrInvalid("set the milestone's billing first (milestone.set_billing)")
	}
	if done, err := invoiced(c, v); err != nil || done {
		if err != nil {
			return nil, err
		}
		return nil, command.ErrConflict("this milestone is already invoiced")
	}
	// Time of supply follows the milestone's completion: the date is the user's, then the release date, then today.
	date := in.InvoiceDate
	if date == nil {
		date = v.releaseDate
	}
	var lines []map[string]any
	var contact string
	var place *string
	if v.quote != nil {
		var number, qcontact *string
		if err := c.Tx.QueryRow(c.Context, "select number, contact_id::text, place_of_supply from billing.quotations where note_id = $1::uuid",
			*v.quote).Scan(&number, &qcontact, &place); err != nil {
			return nil, command.DB(err)
		}
		if qcontact == nil {
			return nil, command.ErrInvalid("the quotation has no customer")
		}
		contact = *qcontact
		type grp struct {
			rate    int
			taxable int64
			sac     *string
		}
		rows, err := c.Tx.Query(c.Context,
			`select gst_rate_bp, sum(taxable_minor)::bigint, min(hsn_sac) from billing.quotation_lines where quotation_id = $1::uuid group by gst_rate_bp order by gst_rate_bp`, *v.quote)
		if err != nil {
			return nil, command.DB(err)
		}
		var gs []grp
		var sum int64
		for rows.Next() {
			var g grp
			if err := rows.Scan(&g.rate, &g.taxable, &g.sac); err != nil {
				rows.Close()
				return nil, command.DB(err)
			}
			gs, sum = append(gs, g), sum+g.taxable
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, command.DB(err)
		}
		if sum <= 0 {
			return nil, command.ErrConflict("the quotation has nothing to bill")
		}
		// One line per GST rate of the quotation, the amount split by taxable share; the last takes the remainder.
		left := *v.amount
		for i, g := range gs {
			alloc := left
			if i < len(gs)-1 {
				alloc = new(big.Int).Div(new(big.Int).Add(new(big.Int).Mul(big.NewInt(*v.amount), big.NewInt(g.taxable)), big.NewInt(sum/2)), big.NewInt(sum)).Int64()
				if alloc > left {
					alloc = left
				}
			}
			left -= alloc
			if alloc <= 0 {
				continue
			}
			d := v.name
			if number != nil {
				d += " (quotation " + *number + ")"
			}
			if len(gs) > 1 {
				d += fmt.Sprintf(" - GST %d.%02d%%", g.rate/100, g.rate%100)
			}
			l := map[string]any{"description": d, "quantity": 1, "unit_price_minor": alloc, "gst_rate_bp": g.rate}
			if g.sac != nil {
				l["hsn_sac"] = *g.sac
			}
			lines = append(lines, l)
		}
	} else {
		p, err := loadProfile(c, v.space)
		if err != nil {
			return nil, err
		}
		if p.ContactID == "" || p.HSNSAC == "" {
			return nil, command.ErrInvalid("set the customer and SAC for this project first (project.billing_set)")
		}
		contact = p.ContactID
		lines = []map[string]any{{"description": v.name, "hsn_sac": p.HSNSAC, "quantity": 1, "unit_price_minor": *v.amount, "gst_rate_bp": p.GSTRateBP}}
	}
	out, invID, err := draft(c, v.space, contact, date, place, lines)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update projects.versions set invoice_id = $2::uuid where id = $1::uuid", v.id, invID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("milestone.invoiced", &invID, map[string]any{"version_id": v.id, "name": v.name, "amount_minor": *v.amount}); err != nil {
		return nil, err
	}
	return map[string]any{"invoice": json.RawMessage(out), "version_id": v.id}, nil
}

func milestoneList(c *command.Ctx, raw json.RawMessage) (any, error) {
	in, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err := need("space_id", in.SpaceID); err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, in.SpaceID, false); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select v.id::text, v.name, v.released, v.release_date::text, v.bill_amount_minor, v.bill_percent_bp, v.quotation_id::text, q.number,
		   case when i.status = 'cancelled' then null else v.invoice_id::text end, i.number, i.status, i.total_minor, i.paid_minor
		 from projects.versions v left join billing.quotations q on q.note_id = v.quotation_id left join billing.invoices i on i.note_id = v.invoice_id
		 where v.space_id = $1::uuid order by v.released, v.release_date nulls last, v.name`, in.SpaceID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var released bool
		var rel, quote, qnum, inv, inum, istatus *string
		var amount, total, paid *int64
		var bp *int
		if err := rows.Scan(&id, &name, &released, &rel, &amount, &bp, &quote, &qnum, &inv, &inum, &istatus, &total, &paid); err != nil {
			return nil, command.DB(err)
		}
		status := "planned"
		switch {
		case inv != nil && istatus != nil && *istatus == "paid":
			status = "paid"
		case inv != nil:
			status = "invoiced"
		case released:
			status = "due"
		}
		out = append(out, map[string]any{"id": id, "name": name, "released": released, "release_date": rel, "billable": amount != nil,
			"bill_amount_minor": amount, "bill_percent_bp": bp, "quotation_id": quote, "quotation_number": qnum, "invoice_id": inv,
			"invoice_number": inum, "invoice_status": istatus, "invoice_total_minor": total, "invoice_paid_minor": paid, "status": status})
	}
	return out, command.DB(rows.Err())
}

// projectMoney: what the project was quoted, invoiced and paid, the advances held, and the hours.
// Credit notes and cancelled or draft invoices are not counted as invoiced.
func projectMoney(c *command.Ctx, raw json.RawMessage) (any, error) {
	in, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err := need("space_id", in.SpaceID); err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, in.SpaceID, false); err != nil {
		return nil, err
	}
	out := map[string]any{"space_id": in.SpaceID}
	one := func(sql string, dest ...any) error {
		return command.DB(c.Tx.QueryRow(c.Context, sql, in.SpaceID).Scan(dest...))
	}
	var quoted, quotedTotal, invoiced, invoicedTotal, received, advApplied, advReceived, advOpen, msPlanned, msInvoiced int64
	var minutes, billable, unbilled int64
	if err := one(`select coalesce(sum(q.subtotal_minor),0), coalesce(sum(q.total_minor),0) from billing.quotations q join core.notes n on n.id = q.note_id
		where n.space_id = $1::uuid and n.deleted_at is null and q.status in ('accepted','converted')`, &quoted, &quotedTotal); err != nil {
		return nil, err
	}
	if err := one(`select coalesce(sum(i.subtotal_minor),0), coalesce(sum(i.total_minor),0) from billing.invoices i join core.notes n on n.id = i.note_id
		where n.space_id = $1::uuid and n.deleted_at is null and i.kind = 'invoice' and i.status not in ('draft','cancelled')`, &invoiced, &invoicedTotal); err != nil {
		return nil, err
	}
	if err := one(`select coalesce(sum(p.amount_minor) filter (where p.method <> 'advance'),0), coalesce(sum(p.amount_minor) filter (where p.method = 'advance'),0)
		from billing.payments p join core.notes n on n.id = p.invoice_id where n.space_id = $1::uuid and n.deleted_at is null`, &received, &advApplied); err != nil {
		return nil, err
	}
	if err := one(`select coalesce(sum(r.amount_minor),0), coalesce(sum(r.amount_minor - r.applied_minor - r.refunded_minor),0)
		from billing.receipts r join core.notes n on n.id = r.note_id where n.space_id = $1::uuid and r.kind = 'receipt'`, &advReceived, &advOpen); err != nil {
		return nil, err
	}
	if err := one(`select coalesce(sum(bill_amount_minor),0), coalesce(sum(bill_amount_minor) filter (where invoice_id is not null),0)
		from projects.versions where space_id = $1::uuid`, &msPlanned, &msInvoiced); err != nil {
		return nil, err
	}
	if err := one(`select coalesce(sum(e.minutes),0), coalesce(sum(e.minutes) filter (where e.billable),0), coalesce(sum(e.minutes) filter (where e.billable and e.invoice_id is null),0)
		from projects.time_entries e join core.notes n on n.id = e.item_id where n.space_id = $1::uuid and n.deleted_at is null`, &minutes, &billable, &unbilled); err != nil {
		return nil, err
	}
	p, err := loadProfile(c, in.SpaceID)
	if err != nil {
		return nil, err
	}
	out["quoted_minor"], out["quoted_total_minor"] = quoted, quotedTotal
	out["invoiced_minor"], out["invoiced_total_minor"] = invoiced, invoicedTotal
	out["received_minor"] = received
	out["advance_received_minor"], out["advance_unapplied_minor"], out["advance_applied_minor"] = advReceived, advOpen, advApplied
	out["milestones_planned_minor"], out["milestones_invoiced_minor"] = msPlanned, msInvoiced
	out["minutes"], out["billable_minutes"], out["unbilled_minutes"] = minutes, billable, unbilled
	out["unbilled_value_minor"] = (p.Rate*unbilled + 30) / 60
	return out, nil
}
