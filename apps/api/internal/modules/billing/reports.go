package billing

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

func reportCommands() []command.Command {
	rd := command.GroupRead
	member := func(c command.Command) command.Command { c.MinRole = command.Member; return c }
	admin := func(c command.Command) command.Command { c.MinRole = command.Admin; return c }
	nc := command.NewCommand
	return []command.Command{
		member(nc("report.receivables_ageing", rd, receivablesAgeing)),
		member(nc("report.gst_summary", rd, gstSummary)),
		member(nc("report.gstr1", rd, gstr1)),
		admin(nc("export.tally", rd, exportTally)),
		admin(nc("ledger.trial_balance", rd, trialBalance)),
	}
}

var buckets = []string{"0-30", "31-60", "61-90", "90+"}

func ageBucket(days int64) string {
	switch {
	case days <= 30:
		return "0-30"
	case days <= 60:
		return "31-60"
	case days <= 90:
		return "61-90"
	}
	return "90+"
}

func asOfInput(input json.RawMessage) (string, error) {
	o, err := parseObj(input)
	if err != nil {
		return "", err
	}
	d, err := o.optDate("as_of")
	if err != nil {
		return "", err
	}
	if d == nil {
		return fmtDate(todayIST()), nil
	}
	return *d, nil
}

func rangeInput(input json.RawMessage) (string, string, error) {
	o, err := parseObj(input)
	if err != nil {
		return "", "", err
	}
	from, err := o.reqDate("from")
	if err != nil {
		return "", "", err
	}
	to, err := o.reqDate("to")
	return from, to, err
}

func receivablesAgeing(c *command.Ctx, input json.RawMessage) (any, error) {
	asOf, err := asOfInput(input)
	if err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select i.note_id::text, i.number, i.contact_id::text, c.title, i.invoice_date::text,
		   i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor
		 from billing.invoices i join core.notes n on n.id = i.note_id left join core.notes c on c.id = i.contact_id
		 where i.kind = 'invoice' and i.status in ('issued','irn_generated','sent','partly_paid')
		   and i.total_minor - i.paid_minor - i.credited_minor - i.written_off_minor > 0
		   and i.invoice_date <= $1::date and n.space_id = any($2::text[]::uuid[])
		   and core.note_access(n.id, $3::uuid, $4, $2::text[]::uuid[]) <> 'none'
		 order by i.invoice_date, i.note_id`, asOf, spaces, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	type tot struct{ amt, n int64 }
	totals := map[string]*tot{}
	for _, b := range buckets {
		totals[b] = &tot{}
	}
	invoices := []map[string]any{}
	var total int64
	asOfT := parseDate(asOf)
	for rows.Next() {
		var id, date string
		var number, contactID, contact *string
		var out int64
		if err := rows.Scan(&id, &number, &contactID, &contact, &date, &out); err != nil {
			return nil, command.DB(err)
		}
		days := daysBetween(asOfT, parseDate(date))
		b := ageBucket(days)
		totals[b].amt += out
		totals[b].n++
		total += out
		invoices = append(invoices, map[string]any{"id": id, "number": number, "contact_id": contactID, "contact": contact,
			"invoice_date": date, "age_days": days, "bucket": b, "outstanding_minor": out})
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	bs := []map[string]any{}
	for _, b := range buckets {
		bs = append(bs, map[string]any{"bucket": b, "outstanding_minor": totals[b].amt, "count": totals[b].n})
	}
	return map[string]any{"as_of": asOf, "buckets": bs, "total_minor": total, "invoices": invoices}, nil
}

// gstSummary is output tax by month and rate; credit notes count negative.
func gstSummary(c *command.Ctx, input json.RawMessage) (any, error) {
	from, to, err := rangeInput(input)
	if err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	rows, err := c.Tx.Query(c.Context,
		`select to_char(i.invoice_date, 'YYYY-MM') as period, l.gst_rate_bp,
		   sum(case when i.kind = 'credit_note' then -l.taxable_minor else l.taxable_minor end)::bigint,
		   sum(case when i.kind = 'credit_note' then -l.cgst_minor else l.cgst_minor end)::bigint,
		   sum(case when i.kind = 'credit_note' then -l.sgst_minor else l.sgst_minor end)::bigint,
		   sum(case when i.kind = 'credit_note' then -l.igst_minor else l.igst_minor end)::bigint
		 from billing.invoice_lines l join billing.invoices i on i.note_id = l.invoice_id
		   join core.notes n on n.id = i.note_id
		 where i.status <> 'draft' and i.invoice_date between $1::date and $2::date and n.space_id = any($3::text[]::uuid[])
		   and core.note_access(n.id, $4::uuid, $5, $3::text[]::uuid[]) <> 'none'
		 group by 1, 2 order by 1, 2`, from, to, spaces, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []map[string]any{}
	var tt, tc, ts, ti int64
	for rows.Next() {
		var period string
		var rate int32
		var t, cg, sg, ig int64
		if err := rows.Scan(&period, &rate, &t, &cg, &sg, &ig); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, map[string]any{"period": period, "gst_rate_bp": rate, "taxable_minor": t, "cgst_minor": cg, "sgst_minor": sg, "igst_minor": ig})
		tt, tc, ts, ti = tt+t, tc+cg, ts+sg, ti+ig
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"from": from, "to": to, "rows": out,
		"totals": map[string]any{"taxable_minor": tt, "cgst_minor": tc, "sgst_minor": ts, "igst_minor": ti}}, nil
}

// ---------- GSTR-1 ----------

// rupees renders paise as a rupee amount the way serde_json prints the f64 parsed from "X.YY" (ryu: "18.0", "1180.5").
func rupees(paise int64) json.Number {
	neg := ""
	a := paise
	if paise < 0 {
		neg = "-"
		a = -paise // i64::MIN would panic in Rust; unreachable for real amounts
	}
	f, _ := strconv.ParseFloat(fmt.Sprintf("%s%d.%02d", neg, a/100, a%100), 64)
	return json.Number(ryuFloat(f))
}

func ryuFloat(f float64) string {
	if math.Abs(f) >= 1e16 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		m, e, _ := strings.Cut(s, "e")
		exp, _ := strconv.Atoi(e)
		return m + "e" + strconv.Itoa(exp)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

var signedDigits = regexp.MustCompile(`^\+?\d+$`)

// monthRange parses YYYY-MM like Rust (i32 year, u32 month, both may carry a '+'); returns first and last day.
func monthRange(period string) (time.Time, time.Time, error) {
	bad := command.ErrInvalid("period must look like 2026-11")
	ys, ms, ok := strings.Cut(period, "-")
	if !ok {
		return time.Time{}, time.Time{}, bad
	}
	if !signedDigits.MatchString(ys) || !signedDigits.MatchString(ms) {
		return time.Time{}, time.Time{}, bad
	}
	y, err := strconv.ParseInt(strings.TrimPrefix(ys, "+"), 10, 32)
	if err != nil {
		return time.Time{}, time.Time{}, bad
	}
	m, err := strconv.ParseUint(strings.TrimPrefix(ms, "+"), 10, 32)
	if err != nil || m < 1 || m > 12 {
		return time.Time{}, time.Time{}, bad
	}
	if y < 1 || y > 9999 {
		// chrono accepts a wider year range; Postgres text dates do not round-trip there. Let Rust answer.
		return time.Time{}, time.Time{}, command.ErrFallback
	}
	start := time.Date(int(y), time.Month(m), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, -1), nil
}

// b2clLimitMinor: Rs 1 lakh from 1 Aug 2024 (Notification 12/2024-CT), Rs 2.5 lakh before.
func b2clLimitMinor(d time.Time) int64 {
	if !d.Before(time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)) {
		return 100_000 * 100
	}
	return 250_000 * 100
}

type docRow struct {
	id, kind, number, date, place string
	total                         int64
	buyer                         *string
	reverse                       bool
}

type rateRow struct {
	invoiceID                 string
	rate                      int32
	taxable, cgst, sgst, igst int64
}

func gstr1(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	period, err := o.reqStr("period")
	if err != nil {
		return nil, err
	}
	from, to, err := monthRange(period)
	if err != nil {
		return nil, err
	}
	spaces, err := access.ReadableSpaceIDs(c)
	if err != nil {
		return nil, err
	}
	uid, admin := access.Viewer(c)
	settings, err := workspaceSettings(c)
	if err != nil {
		return nil, err
	}
	supplierState, _ := settingsMap(settings).str("state_code")
	rows, err := c.Tx.Query(c.Context,
		`select i.note_id::text, i.kind, i.number, i.invoice_date::text, i.total_minor, i.place_of_supply, i.buyer_gstin, i.reverse_charge
		 from billing.invoices i join core.notes n on n.id = i.note_id
		 where i.number is not null and i.invoice_date between $1::date and $2::date and n.space_id = any($3::text[]::uuid[])
		   and core.note_access(n.id, $4::uuid, $5, $3::text[]::uuid[]) <> 'none'
		 order by i.number`, fmtDate(from), fmtDate(to), spaces, uid, admin)
	if err != nil {
		return nil, command.DB(err)
	}
	var docs []docRow
	ids := []string{}
	for rows.Next() {
		var d docRow
		if err := rows.Scan(&d.id, &d.kind, &d.number, &d.date, &d.total, &d.place, &d.buyer, &d.reverse); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		docs = append(docs, d)
		ids = append(ids, d.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	rrows, err := c.Tx.Query(c.Context,
		`select invoice_id::text, gst_rate_bp, sum(taxable_minor)::bigint, sum(cgst_minor)::bigint,
		   sum(sgst_minor)::bigint, sum(igst_minor)::bigint
		 from billing.invoice_lines where invoice_id = any($1::text[]::uuid[]) group by invoice_id, gst_rate_bp order by gst_rate_bp`, ids)
	if err != nil {
		return nil, command.DB(err)
	}
	var rates []rateRow
	for rrows.Next() {
		var r rateRow
		if err := rrows.Scan(&r.invoiceID, &r.rate, &r.taxable, &r.cgst, &r.sgst, &r.igst); err != nil {
			rrows.Close()
			return nil, command.DB(err)
		}
		rates = append(rates, r)
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return nil, command.DB(err)
	}

	items := func(id string) []any {
		out := []any{}
		for _, r := range rates {
			if r.invoiceID == id {
				out = append(out, map[string]any{"num": len(out) + 1, "itm_det": map[string]any{"rt": rupees(int64(r.rate)),
					"txval": rupees(r.taxable), "camt": rupees(r.cgst), "samt": rupees(r.sgst), "iamt": rupees(r.igst)}})
			}
		}
		return out
	}
	b2b, cdnr, b2cl := map[string][]any{}, map[string][]any{}, map[string][]any{}
	type csKey struct {
		pos  string
		rate int32
	}
	b2cs := map[csKey]*[4]int64{}
	for _, d := range docs {
		dt := parseDate(d.date).Format("02-01-2006")
		rchrg := "N"
		if d.reverse {
			rchrg = "Y"
		}
		switch {
		case d.buyer != nil && d.kind == "invoice":
			b2b[*d.buyer] = append(b2b[*d.buyer], map[string]any{"inum": d.number, "idt": dt, "val": rupees(d.total), "pos": d.place,
				"rchrg": rchrg, "inv_typ": "R", "itms": items(d.id)})
		case d.buyer != nil:
			cdnr[*d.buyer] = append(cdnr[*d.buyer], map[string]any{"ntty": "C", "nt_num": d.number, "nt_dt": dt, "val": rupees(d.total),
				"pos": d.place, "rchrg": rchrg, "itms": items(d.id)})
		case d.kind == "invoice" && supplierState != "" && d.place != supplierState && d.total > b2clLimitMinor(parseDate(d.date)):
			b2cl[d.place] = append(b2cl[d.place], map[string]any{"inum": d.number, "idt": dt, "val": rupees(d.total), "itms": items(d.id)})
		default:
			sign := int64(1)
			if d.kind == "credit_note" {
				sign = -1
			}
			for _, r := range rates {
				if r.invoiceID != d.id {
					continue
				}
				e := b2cs[csKey{d.place, r.rate}]
				if e == nil {
					e = &[4]int64{}
					b2cs[csKey{d.place, r.rate}] = e
				}
				for i, v := range [4]int64{r.taxable, r.cgst, r.sgst, r.igst} {
					e[i] += sign * v
				}
			}
		}
	}
	wrap := func(m map[string][]any, key string) []any {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []any{}
		for _, k := range keys {
			out = append(out, map[string]any{"ctin": k, key: m[k]})
		}
		return out
	}
	cl := []any{}
	poss := make([]string, 0, len(b2cl))
	for p := range b2cl {
		poss = append(poss, p)
	}
	sort.Strings(poss)
	for _, p := range poss {
		cl = append(cl, map[string]any{"pos": p, "inv": b2cl[p]})
	}
	ckeys := make([]csKey, 0, len(b2cs))
	for k := range b2cs {
		ckeys = append(ckeys, k)
	}
	sort.Slice(ckeys, func(i, j int) bool {
		if ckeys[i].pos != ckeys[j].pos {
			return ckeys[i].pos < ckeys[j].pos
		}
		return ckeys[i].rate < ckeys[j].rate
	})
	cs := []any{}
	for _, k := range ckeys {
		a := b2cs[k]
		ty := "INTER"
		if k.pos == supplierState {
			ty = "INTRA"
		}
		cs = append(cs, map[string]any{"sply_ty": ty, "pos": k.pos, "typ": "OE", "rt": rupees(int64(k.rate)),
			"txval": rupees(a[0]), "camt": rupees(a[1]), "samt": rupees(a[2]), "iamt": rupees(a[3])})
	}
	gstin := settings["gstin"]
	if gstin == nil {
		gstin = json.RawMessage("null")
	}
	return map[string]any{"gstin": gstin, "fp": fmt.Sprintf("%02d%d", int(from.Month()), from.Year()),
		"b2b": wrap(b2b, "inv"), "b2cl": cl, "b2cs": cs, "cdnr": wrap(cdnr, "nt")}, nil
}

// ---------- Tally ----------

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func xmlEscape(s string) string { return xmlEscaper.Replace(s) }

func tallyAmount(paise int64) string {
	neg := ""
	a := paise
	if paise < 0 {
		neg, a = "-", -paise
	}
	return fmt.Sprintf("%s%d.%02d", neg, a/100, a%100)
}

// exportTally is Tally import XML: one voucher per journal entry; debits use ISDEEMEDPOSITIVE=Yes with a negative amount.
func exportTally(c *command.Ctx, input json.RawMessage) (any, error) {
	from, to, err := rangeInput(input)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select e.id::text, e.source_type, e.posted_at, e.memo, i.number, c.title
		 from billing.journal_entries e
		   left join billing.payments p on e.source_type = 'payment' and p.id = e.source_id
		   left join billing.invoices i on i.note_id = case when e.source_type = 'payment' then p.invoice_id else e.source_id end
		   left join core.notes c on c.id = i.contact_id
		 where e.posted_at >= $1::date and e.posted_at < $2::date + 1 order by e.posted_at, e.id`, from, to)
	if err != nil {
		return nil, command.DB(err)
	}
	type voucher struct {
		id, sourceType      string
		postedAt            time.Time
		memo, number, party *string
	}
	var vs []voucher
	ids := []string{}
	for rows.Next() {
		var v voucher
		if err := rows.Scan(&v.id, &v.sourceType, &v.postedAt, &v.memo, &v.number, &v.party); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		vs = append(vs, v)
		ids = append(ids, v.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	lrows, err := c.Tx.Query(c.Context,
		`select l.entry_id::text, a.name, l.debit_minor, l.credit_minor from billing.journal_lines l
		   join billing.accounts a on a.code = l.account_code and a.workspace_id = l.workspace_id
		 where l.entry_id = any($1::text[]::uuid[]) order by l.id`, ids)
	if err != nil {
		return nil, command.DB(err)
	}
	type vline struct {
		entry, name   string
		debit, credit int64
	}
	var ls []vline
	for lrows.Next() {
		var l vline
		if err := lrows.Scan(&l.entry, &l.name, &l.debit, &l.credit); err != nil {
			lrows.Close()
			return nil, command.DB(err)
		}
		ls = append(ls, l)
	}
	lrows.Close()
	if err := lrows.Err(); err != nil {
		return nil, command.DB(err)
	}
	var xml strings.Builder
	xml.WriteString("<ENVELOPE><HEADER><TALLYREQUEST>Import Data</TALLYREQUEST></HEADER><BODY><IMPORTDATA>" +
		"<REQUESTDESC><REPORTNAME>Vouchers</REPORTNAME></REQUESTDESC><REQUESTDATA>\n")
	for _, v := range vs {
		vtype := "Journal"
		switch v.sourceType {
		case "invoice":
			vtype = "Sales"
		case "credit_note":
			vtype = "Credit Note"
		case "payment":
			vtype = "Receipt"
		}
		date := v.postedAt.UTC().Add(330 * time.Minute).Format("20060102")
		fmt.Fprintf(&xml, "<TALLYMESSAGE xmlns:UDF=\"TallyUDF\"><VOUCHER VCHTYPE=\"%s\" ACTION=\"Create\">"+
			"<DATE>%s</DATE><VOUCHERTYPENAME>%s</VOUCHERTYPENAME><VOUCHERNUMBER>%s</VOUCHERNUMBER>"+
			"<PARTYLEDGERNAME>%s</PARTYLEDGERNAME><NARRATION>%s</NARRATION>",
			vtype, date, vtype, xmlEscape(deref(v.number)), xmlEscape(deref(v.party)), xmlEscape(deref(v.memo)))
		for _, l := range ls {
			if l.entry != v.id {
				continue
			}
			positive, amount := "No", l.credit
			if l.debit > 0 {
				positive, amount = "Yes", -l.debit
			}
			fmt.Fprintf(&xml, "<ALLLEDGERENTRIES.LIST><LEDGERNAME>%s</LEDGERNAME><ISDEEMEDPOSITIVE>%s</ISDEEMEDPOSITIVE>"+
				"<AMOUNT>%s</AMOUNT></ALLLEDGERENTRIES.LIST>", xmlEscape(l.name), positive, tallyAmount(amount))
		}
		xml.WriteString("</VOUCHER></TALLYMESSAGE>\n")
	}
	xml.WriteString("</REQUESTDATA></IMPORTDATA></BODY></ENVELOPE>")
	return map[string]any{"vouchers": len(vs), "xml": xml.String()}, nil
}

// ---------- trial balance ----------

func trialBalance(c *command.Ctx, input json.RawMessage) (any, error) {
	asOf, err := asOfInput(input)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select a.code, a.name, a.kind, coalesce(sum(l.debit_minor), 0)::bigint, coalesce(sum(l.credit_minor), 0)::bigint
		 from billing.accounts a
		   left join (select l.* from billing.journal_lines l join billing.journal_entries e on e.id = l.entry_id
		              where e.posted_at < $1::date + 1) l on l.account_code = a.code
		 group by a.code, a.name, a.kind order by a.code`, asOf)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	accounts := []map[string]any{}
	var d, cr int64
	for rows.Next() {
		var code, name, kind string
		var deb, cre int64
		if err := rows.Scan(&code, &name, &kind, &deb, &cre); err != nil {
			return nil, command.DB(err)
		}
		accounts = append(accounts, map[string]any{"code": code, "name": name, "kind": kind,
			"debit_minor": deb, "credit_minor": cre, "balance_minor": deb - cre})
		d, cr = d+deb, cr+cre
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"as_of": asOf, "accounts": accounts, "total_debit_minor": d, "total_credit_minor": cr, "net_minor": d - cr}, nil
}
