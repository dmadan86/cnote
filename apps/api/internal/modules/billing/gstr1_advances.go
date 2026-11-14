package billing

import (
	"time"

	"github.com/cnote/api/internal/command"
)

// advanceTables are GSTR-1 table 11A (advances received in the month, net of refund vouchers) and 11B (advances
// adjusted against invoices in the month), grouped by place of supply and rate. Only services advances carry GST.
// An advance counts in 11B in the month of the invoice, or of the application when that is later, so an already
// filed month never changes. Rows use the GST portal shape: pos, sply_ty and itms[rt, ad_amt (taxable value), camt, samt, iamt].
func advanceTables(c *command.Ctx, from, to time.Time, spaces []string, uid *string, admin bool, supplierState string) (received, adjusted []any, err error) {
	run := func(q string) ([]any, error) {
		rows, err := c.Tx.Query(c.Context, q, fmtDate(from), fmtDate(to), spaces, uid, admin)
		if err != nil {
			return nil, command.DB(err)
		}
		defer rows.Close()
		var order []string
		groups := map[string][]any{}
		for rows.Next() {
			var pos string
			var rate int32
			var taxable, cg, sg, ig int64
			if err := rows.Scan(&pos, &rate, &taxable, &cg, &sg, &ig); err != nil {
				return nil, command.DB(err)
			}
			if _, ok := groups[pos]; !ok {
				order = append(order, pos)
			}
			groups[pos] = append(groups[pos], map[string]any{"rt": rupees(int64(rate)), "ad_amt": rupees(taxable),
				"camt": rupees(cg), "samt": rupees(sg), "iamt": rupees(ig)})
		}
		if err := rows.Err(); err != nil {
			return nil, command.DB(err)
		}
		out := []any{}
		for _, pos := range order {
			ty := "INTER"
			if pos != "" && pos == supplierState {
				ty = "INTRA"
			}
			out = append(out, map[string]any{"pos": pos, "sply_ty": ty, "itms": groups[pos]})
		}
		return out, nil
	}
	received, err = run(`
		select coalesce(r.place_of_supply,''), r.gst_rate_bp,
		  sum(case r.kind when 'receipt' then 1 else -1 end * r.taxable_minor)::bigint,
		  sum(case r.kind when 'receipt' then 1 else -1 end * r.cgst_minor)::bigint,
		  sum(case r.kind when 'receipt' then 1 else -1 end * r.sgst_minor)::bigint,
		  sum(case r.kind when 'receipt' then 1 else -1 end * r.igst_minor)::bigint
		from billing.receipts r join core.notes n on n.id = r.note_id
		where r.supply_type = 'services' and n.deleted_at is null and r.received_at between $1::date and $2::date
		  and n.space_id = any($3::text[]::uuid[]) and core.note_access(n.id, $4::uuid, $5, $3::text[]::uuid[]) <> 'none'
		group by 1, 2 order by 1, 2`)
	if err != nil {
		return nil, nil, err
	}
	adjusted, err = run(`
		select coalesce(r.place_of_supply,''), r.gst_rate_bp, sum(a.taxable_minor)::bigint, sum(a.cgst_minor)::bigint,
		  sum(a.sgst_minor)::bigint, sum(a.igst_minor)::bigint
		from billing.receipt_applications a
		  join billing.receipts r on r.note_id = a.receipt_id
		  join billing.invoices i on i.note_id = a.invoice_id
		  join core.notes n on n.id = r.note_id
		where r.supply_type = 'services' and n.deleted_at is null
		  and greatest(i.invoice_date, (a.applied_at at time zone 'Asia/Kolkata')::date) between $1::date and $2::date
		  and n.space_id = any($3::text[]::uuid[]) and core.note_access(n.id, $4::uuid, $5, $3::text[]::uuid[]) <> 'none'
		group by 1, 2 order by 1, 2`)
	return received, adjusted, err
}
