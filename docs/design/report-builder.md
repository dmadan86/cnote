# Report builder v1: spec (C Note)

Sources: Fable research (competitors + code design) and Mobbin study (2026-11-18).
Mobbin references: Attio New report (builder panel), Klaviyo New report (question templates), Toggl report detail (breakdown table with total), Linear filter chips, Upwork weekly summary (presets with resolved dates), Xero analytics dashboards (one period control), Sentry Add to dashboard, Typeform Download (export), Jobber iOS Business health (mobile).

## Scope v1
- Report library: question-phrased templates, saved reports, existing billing reports listed as "Standard" (links).
- Builder: Report on (dataset) → Show (columns) → Only include (filter chips, AND only) → Group by (≤2, date bucket day/week/month/quarter/FY) → Add up (count/sum/avg/min/max) → Show as (table | bar | line | number). Live preview, no Run button.
- Period presets (IST, FY from company fy_start_month, default April): this_month, last_month, this_quarter, last_quarter, this_fy, last_fy, fy_to_date, last_12_months, custom.
- Save (private | workspace), dashboards (ordered tiles, sizes number|half|full, one period override), CSV export, drill-down from a grouped row to the underlying rows.
- ₹ compact on tiles/bars (Intl en-IN compact), money() in tables.
- Out of v1: schedules/email, pivot, AND/OR groups, formulas, joins across datasets, XLSX, server PDF (use print), AI ask, mobile builder.

## Datasets v1 (static Go registry)
invoices, payments, purchase_bills, expenses, deals, tickets, tasks, time_entries.
Each: key, label, FROM/joins, scope predicate (copied from the module's list command: readable spaces + core.note_access, expenses own/admin/finance rule, timelog readable spaces), module switch key, finance flag (dataset- or field-level), fields {key,label,type(text|enum|date|money|number|bool|person|contact),sql,aggs,enum values}.

## Spec JSON
{ "dataset":"invoices",
  "columns":["contact","invoice_date","total"],
  "filters":[{"field":"status","op":"in","value":["issued","paid"]}],
  "period":{"field":"invoice_date","preset":"this_fy"} | {"field":..,"from":"2026-04-01","to":"2027-03-31"},
  "group_by":[{"field":"contact"},{"field":"invoice_date","bucket":"month"}],
  "measures":[{"field":"total","agg":"sum"},{"agg":"count"}],
  "sort":[{"key":"m0","dir":"desc"}],
  "limit":2000,
  "viz":"table|bar|line|number" }
Ops by type: text eq/neq/contains/empty/not_empty; enum in/not_in; date between/before/after; money,number eq/gt/gte/lt/lte/between; bool is; person/contact in.

## Commands
report.datasets (read) → [{key,label,fields:[...]}] only those the caller may use.
report.run (read) {spec} | {id, period?} → {columns:[{key,label,type}], rows:[[...]], total_row?, truncated, period:{from,to,label}}; {format:"csv"} → {csv, filename}.
report.save (write) {id?, name, spec, visibility} ; report.get ; report.list ; report.delete (destructive, owner/admin).
dashboard.save {id?, name, tiles:[{report_id,size}], period?} ; dashboard.get ; dashboard.list ; dashboard.delete.
Limits: 2000 rows screen, 10000 CSV, 1 group dim, 6 measures, 20 filters, statement_timeout 5s (set_config local).
Security: identifiers only from registry; values bound; scope predicate always ANDed; finance checked via c.FinanceAllowed per dataset/field; saved spec re-validated on every run with the *caller's* rights; never select payroll PII.

## Storage: migrations/1230_reports.sql — core.reports, core.dashboards (tenant RLS).

## Changes after Codex design review (binding)
- Group by: ONE field (date fields may bucket day/week/month/quarter/fy). Viz: table | bar | number only (no line).
- Dashboard reorder: up/down buttons (keyboard accessible), no drag. Tile sizes: number | half | full.
- Totals computed over the full filtered population (separate query), never over the truncated rows. Truncation shown explicitly; CSV marks incomplete exports (last line "# truncated at N rows").
- Every dataset has one row grain; only many-to-one joins (no sum multiplication).
- Periods resolved server-side in IST with the workspace FY; inclusive dates; timestamps compared as (col at time zone 'Asia/Kolkata')::date.
- report.run / report.datasets / report.list / report.get / dashboard.get / dashboard.list: MinRole Member (NOT Guest). Guests get Forbidden.
- Field permissions apply to columns, filters, group, measures, sort, period field, drill-down and CSV.
- Saved objects: private = owner only; workspace = all members can view/run; only owner or admin edit/delete. A dashboard tile pointing at a report the caller cannot see renders as "Not available to you" (tile returns {error}). Runs always use the caller's rights.
- Drill-down: report.run with the same spec + an extra eq/in filter on the group field (and period), in row mode with the dataset's default columns.
- Builder UX: progressive disclosure. Top: "What do you want to see?" (template or dataset) + period + filters. Below, collapsible "Summarise": Group by + measures phrased "Total amount", "Number of invoices", "Average amount". Then "Show as". Preview debounced 400 ms, previous result kept while loading, AbortController on stale requests.
- Above each result: resolved dates, active filters, "Showing only what you can access" note, truncation warning.
- Validate the "Who owes me money?" template totals against report.receivables_ageing in a server test.

## Command contract (JSON)
- report.datasets {} → {datasets:[DatasetInfo]}
- report.run {spec} | {id, period?:Period} ; optional "format":"csv" → {csv, filename} ; optional "drill":{"value":…} handled client-side as an extra filter (no server param).
  → {columns, rows, totals, count, truncated, period}
- report.save {id?, name, spec, visibility:"private"|"workspace"} → {report}
- report.get {id} → {report}   report = {id,name,dataset,spec,visibility,created_by,owner_name,created_at,updated_at,can_edit}
- report.list {} → {reports:[report without spec? -> include spec]}
- report.delete {id} → {ok:true}           (GroupDestructive)
- dashboard.save {id?, name, tiles:[{report_id,size}], period?:Period, visibility} → {dashboard}
- dashboard.get {id} → {dashboard}  dashboard = {id,name,tiles,period,visibility,created_by,created_at,updated_at,can_edit}
- dashboard.list {} → {dashboards:[…]}
- dashboard.delete {id} → {ok:true}
Groups: datasets/run/get/list = GroupRead with MinRole Member; save = GroupWriteNotes; delete = GroupDestructive.
Each command gets Description + Schema in modules/mcpmeta.go (MCP).

## Templates v1 (web-side constants, spec JSON)
1. Who owes me money? — invoices, filter status in issued/partially_paid (use real enum values), group by contact, measure sum(balance due), viz bar.
2. Who are my top customers? — invoices, period this_fy on invoice_date, group contact, sum(total), bar, sort m0 desc.
3. Where did our time go? — time_entries, period this_month, group project, sum(hours), bar.
4. What is in my pipeline? — deals, filter open stages, group stage, sum(amount)+count, table.
5. Revenue by month — invoices, this_fy, group invoice_date bucket month, sum(total), bar.
6. What are we spending on? — expenses, this_fy, group category, sum(amount), bar.
7. Open tickets by priority — tickets, filter not resolved, group priority, count, table.
8. Overdue tasks by person — tasks, filter due before today & not done, group assignee, count, bar.
(Use only fields the catalog really has; adjust names to match datasets.go.)
