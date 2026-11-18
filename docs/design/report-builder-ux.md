# Report builder: UX (from Mobbin research, 2026-11-18, adjusted by the Codex review in report-builder.md)

## Existing C Note design language (match it)
- No component library: shared Tailwind v4 class strings and native elements in `apps/web/src/components/ui.tsx` (btn, btnPrimary, inputCls, Field, Modal on native <dialog>, Pill, Avatar, ErrorText). Native selects/date inputs. lucide-react icons 13-16px.
- Tokens in `apps/web/src/app/globals.css`: white canvas, #f7f7f8 sidebars, #e4e4e7 hairlines, indigo #4f46e5 accent, ok/warn/bad pairs, 6px radius, dark mode via prefers-color-scheme.
- Type: 14px body, 13px controls, 12px muted labels/table headers, text-xl font-semibold page titles and KPI values.
- Pages: px-6 py-6, underline SubNav tabs (`components/billing/bits.tsx`).
- Tables: borderless, hairline row dividers, hover:bg-hover, right-aligned tabular-nums money, bold Total row. Cards `rounded-lg border border-line p-4`.
- KPI tile: `components/home/dashboard.tsx`.
- Existing reports: `components/billing/reports.tsx` (RangeInputs = two native date inputs, `money()` → ₹1,18,000.00, `download()` helper).
- Saved-view chips and segmented toggles: `components/projects/space-view.tsx`. SVG chart style: `components/projects/sprints.tsx`.
- No chart library: bars are CSS widths. Do not add dependencies.
- Compact rupees: `new Intl.NumberFormat('en-IN',{style:'currency',currency:'INR',notation:'compact',maximumFractionDigits:1})` → ₹12.5L, ₹3.5Cr (tiles, bar labels). Tables/CSV use money().

## Flow
Rail "Reports" → library → pick a question template (or "Start from scratch") → builder opens with real data → Save (name prefilled from the summary sentence, visibility, optional "Add to dashboard") → dashboard.

## Library (route /w/[ws]/reports)
```
Reports                                               [+ New report]
 Reports | Dashboards
+- Start from a template ------------------------------------------+
| Who owes me money?  | Who are my top     | Where did our time  |  ...
| Unpaid invoices by  | customers?         | go? Hours by project|
| customer            | Revenue, This FY   |                     |
+------------------------------------------------------------------+
 (All) (Money) (Sales) (Projects) (Support)        [Search reports]
 Name                       Report on     Shared        Updated
 Revenue by customer        Invoices      Workspace     2 Nov
 Receivables ageing [Standard]  → links to /w/[ws]/billing/reports
```
Refs: Klaviyo New report (question templates), Gusto Reports (recommended cards above table), Fresha Reports home (category pills).

## Builder (route /w/[ws]/reports/new and /w/[ws]/reports/[id])
```
< Reports / Revenue by customer (rename)        [Export CSV] [Save]
 Total invoiced by customer · This FY (1 Apr 2026 – 31 Mar 2027)
+- What do you want to see? ------+ +- Preview -------------------------+
| Report on [Invoices        v]   | | ₹48.2L total · 37 customers       |
| Period [This FY v] on [Inv date]| | Customer        Invoices   Amount |
| Only include                    | | Asha Interiors        12 12,40,000|
|  (Status is Issued, Paid x)     | | ...                               |
|  + Filter                       | | Total                 86 48,20,300|
| > Summarise                     | | Showing only what you can access  |
|   Group by [Customer v]         | | 3 rows hidden by filters          |
|   Show  [Total amount v] + Add  | +-----------------------------------+
| Show as (Table)(Bar)(Number)    |
| (row mode: Columns chips + Add) |
+---------------------------------+
```
- Progressive disclosure: "Summarise" collapsed until used (templates open it filled).
- Measure labels in owner words: "Number of <rows>", "Total <field>", "Average <field>", "Highest/Lowest <field>".
- Charts that don't fit are disabled with a reason (Attio): Number needs one measure and no group; Bar needs a group.
- Filter chips (Linear / Notion): click chip → small popover with operator + value; enum = multi-select checkboxes; "is any of".
- Period presets show resolved dates under each label (Upwork). Custom = two native date inputs (reuse RangeInputs pattern).
- Live preview: debounce 400 ms, AbortController, keep previous result while loading, inline error.
- Drill-down: clicking a grouped row / bar opens a side sheet (Modal) with the underlying rows (Airtable), rows link to the record where a route exists.
- Truncation: "Showing first 2,000 of 5,312 rows. Totals include all rows. Narrow the filters or export CSV."
Refs: Attio New report, Mailchimp custom report, Notion chart settings, Toggl report detail, Stripe billing collections, Airtable drill-down, Linear filter chips, Upwork presets, Typeform Download.

## Dashboard (route /w/[ws]/reports/dashboards/[id])
```
My dashboard v     Period [Use each report's v]  [+ Add report] [Edit]
+ Revenue -----+ + Unpaid -----+ + Hours ------+ + Open deals --+   (size=number)
| ₹48.2L       | | ₹6.4L        | | 1,240 h      | | ₹22L · 14     |
+--------------+ +--------------+ +--------------+ +---------------+
+ Revenue by customer · This FY ----+ + Unpaid by age · today -------+ (size=half)
| Asha   ############  ₹12.4L       | | ...                          |
| Open report >                     | |                              |
+-----------------------------------+ +------------------------------+
+ Hours by project · This month (full-width table) -----------------+ (size=full)
```
- One period control overrides tiles whose report has a period; each tile subtitle states its period (Xero).
- Edit mode: per tile ↑ ↓ buttons, size select, remove. No drag.
- Add report: modal listing saved reports with a one-line description (ClickUp/Stripe add chart).
- Tile for a report the viewer can't access: "Not available to you".
- Mobile width (<640px): single column, tiles full width (Jobber iOS business health, Revolut Business).
Refs: Xero analytics dashboards, Shopify customize analytics, Airtable interface dashboard, Sentry add to dashboard.

## Anti-patterns (do not do)
Chart-type zoo; analyst words (Metrics/Rows/Where/Having); 45-checkbox column walls (default 4-6 columns, search the rest); form first data last; nested AND/OR; blank free-form grid; raw transform chips; calendar-year defaults; ₹1,250,000 western grouping; colour-only signals; a separate Run button; widgets that don't state their period.
