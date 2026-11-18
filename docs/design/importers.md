# Importers: contacts, items, opening balances, Tally — v1 spec (18 Nov 2026)

Sources: Fable domain + code research and Fable Mobbin UX study (2026-11-18); owner approved the lead's cuts (CSV + Tally XML only, skip-existing duplicates, undo removes added rows, masters only from Tally). Service boundaries: docs/architecture/service-boundaries.md — the importer module writes other modules' data only through their registry commands (`c.Call`) or narrowly through documented import commands those modules own; no new cross-module Go imports (TestModuleBoundaries).

## Scope v1
1. **Contacts** (customers and vendors) from CSV: name, kind (customer/vendor/both), email, phone, company, GSTIN, PAN, state (derived from GSTIN when blank), billing address.
2. **Items** from CSV: SKU (generated from name when missing), name, HSN/SAC, GST rate (0/5/12/18/28/40 → bp; no silent default — missing rate is an error), unit, sale price, opening stock qty + value per location.
3. **Opening balances** (one page, not a file wizard): go-live date (default first day of FY), account balances grid (Dr/Cr) with live difference posted to **3900 Opening balance adjustments** (equity), unpaid customer invoices and unpaid vendor bills as **opening documents** (CSV or rows), Post → one journal entry dated the day before go-live; undo = reversal entry.
4. **From Tally**: upload TallyPrime/ERP9 masters XML (Export → Masters → All Masters → XML). Parse ledgers (Sundry Debtors → customers, Sundry Creditors → vendors, others → accounts with opening balances, groups mapped by primary group + ISREVENUE/ISDEEMEDPOSITIVE), stock items (+ units, godowns, opening qty/value). "What we found" summary → choose what to bring in → contacts and items imported like CSV; balances fill the opening-balances draft (never post directly). Vouchers are not imported.

Out of v1: XLSX (needs Go 1.26 for excelize), inline cell editing in review (download error rows instead), duplicate "replace"/"fill blanks" policies (v1: skip existing and list them), Tally vouchers / bill-wise detail, batches/serials, Hindi UI, mobile.

## Flow (UX from Mobbin study: Attio/Apollo/Xero/Melio/Pipedrive/Linear patterns)
Import hub (Settings → Workspace → Import data, and Import buttons on Contacts and Items lists): cards Contacts, Items, Opening balances, From Tally, plus existing Jira/Notion/Trello/CSV-pages. Recent imports list with status and Undo.
Wizard (Contacts, Items): **Upload** (CSV, sample file download, header row auto-detected, row count) → **Match columns** (auto-matched by header keywords incl. Tally/Vyapar names; show only unmatched by default; DD-MM-YYYY) → **Review** (tabs Ready / Needs fixing / Already in C Note with the reason per row; "Download rows that need fixing" CSV) → **Import** (background job; can leave the page) → **Done** (added / skipped counts, View, Undo for 7 days).
Opening balances: single page as above with Confirm dialog restating date, totals, difference.
Tally: steps with export instructions → upload XML → what we found (counts per type, warnings like "9 items have no HSN") → choose → review per type → import.
Plain Indian terms (GSTIN, HSN, Dr/Cr, Sundry Debtors), ₹ en-IN formatting, tokens only, EmptyState, settings design language.

## Data rules
- Validation: GSTIN via existing billing GSTIN checksum helper (format check, never "verified"); PAN regex + matches GSTIN chars 3–12; state from GSTIN; HSN 4/6/8 digits; qty ≤ 3 decimals; 5,000 rows per file; 10 MB per file; UTF-8 and UTF-16 (Tally) input; strip `&#4;` and control bytes from Tally XML.
- Duplicates (skip + list): contacts by GSTIN, else normalised name + phone/email; items by SKU (case-insensitive), else name.
- Opening documents: billing.invoices and billing.purchase_bills gain `opening boolean not null default false`; opening docs are inserted issued with one line "Opening balance" at 0% GST, original number/date/due date, outstanding = amount; excluded from GST reports (GSTR-1, GST summary, ITC summary), IRN window sweep, credit notes and cancel; payments and ageing work as normal. Party balances come ONLY from opening documents (account grid refuses 1100/2100 to prevent double counting). Opening bill TDS default 0.
- Opening stock: inventory receipt movements with source_type 'import' and the job id (reversible).
- Accounts: seed 3900 Opening balance adjustments (equity) per workspace on first use; new accounts created from Tally groups get codes in their kind's range.
- Every imported row records its import job id (importers.rows target_id) so Undo can find it. Undo: contacts soft-deleted only if unused on any document; items deactivated; stock and journal reversed; opening documents cancelled only if no payment recorded. Undo window 7 days; refused after.
- Roles: Admin only for all import commands; opening balances additionally finance access. data_import.* prefix in financePrefixes. Audit events data_import.started/completed/undone.

## Commands
- `POST /api/w/{ws}/imports?kind=contacts|items|opening_docs|tally` raw upload (≤10 MB) → creates job in `preview` with parsed rows.
- `data_import.template {kind}` → {fields:[{key,label,required,example}], sample_csv}
- `data_import.preview {id, mapping?, options?}` → {columns:[{header,samples[],field?}], counts:{ready,fix,existing}, rows (first 200 with status+reason), tally?:{found:{customers,vendors,ledgers,items,units,godowns}, warnings[]}}
- `data_import.run {id, include?}` → queues job kind data_import.run (worker, 500 rows per tx, savepoint per row)
- `data_import.status {id}` / `data_import.list {}` (extend existing import.list/status if natural)
- `data_import.errors {id}` → {csv, filename}
- `data_import.undo {id}` (Confirm) → reverses
- `opening.get {}` / `opening.save {go_live, lines:[{account_code, dr_minor, cr_minor}]}` (draft) / `opening.post {}` (Confirm) / `opening.reverse {}`
Migration: `migrations/1260_imports.sql` (importers.jobs gains kind + 'preview'/'undone' statuses — confirm constraint names with \d; importers.rows; opening columns; partial unique index for invoice numbers that ignores cancelled opening docs; movements source_type 'import'; core.opening_balances draft table; 3900 account seed function).

## Tests
Parser unit tests with fixture files (CSV variants incl. Tally-style headers, DD-MM-YYYY, Indian amounts "1,18,000.00", Dr/Cr suffix; Tally XML UTF-8 and UTF-16 with `&#4;`, Prime 2 flat tags and Prime 3+ nested GST tags); validator tests; server tests for each kind incl. duplicates, undo, opening balances difference → 3900, opening docs excluded from GSTR-1, roles/finance, cross-tenant; e2e: contacts CSV wizard end-to-end, opening balances post, Tally upload with a fixture.

## Binding revisions after Codex design review
1. **One atomic opening posting.** `opening.post` creates ONE journal entry dated the day before go-live containing: every grid account line, Dr 1100 per opening invoice (sum), Cr 2100 per opening bill (sum), and the balancing line to 3900. Opening documents themselves never post to the ledger (no revenue/expense/GST lines): their insert path skips the normal invoice/bill posting. `opening.post` is unique per workspace (refuse a second active post); `opening.reverse` posts the exact negation once and marks the posting reversed.
2. **GST exclusion inventory.** Before coding, list every query that reads billing.invoices / purchase_bills for tax (GSTR-1, GST summary, ITC summary, TDS reports, Tally export, report builder datasets, e-invoice IRN sweep) and exclude `opening` rows in each, with one test per path asserting opening docs are absent.
3. **Undo is all-or-nothing.** Lock the job, preflight every dependency (contact used on any document, stock already consumed below the imported qty, opening doc with a payment, opening posting already reversed/posted) and either reverse the whole job in one transaction or refuse with per-row reasons. No partial undo.
4. **Idempotent runs.** importers.rows has a unique (job_id, row_no); each row's apply writes target_id + status in the same transaction as the created record; the run claims the job with a state CAS (preview → running) and resumes only rows without a status; a second run/concurrent worker is a no-op.
5. **Gates.** Upload, preview, errors download, run and undo: Admin + finance access for items with cost/value, opening docs, opening balances and Tally (balances); contacts: Admin. Add both `data_import.` and `opening.` prefixes to financePrefixes where finance applies (or check finance in-handler per kind). Worker applies rows as the requesting admin principal (not System), tenant RLS on all tables.
6. **Untrusted input.** Error CSV cells starting with = + - @ tab/CR are prefixed with ' (reuse payroll csvSafe). XML: reject DOCTYPE/DTD, cap depth and total size (10 MB), streaming decoder, no external entities. All imported text rendered escaped in web.
7. **Dates.** Document and go-live dates are calendar dates (YYYY-MM-DD, DD-MM-YYYY accepted on input); FY default from the workspace fy_start_month in IST; the 7-day undo window is a UTC timestamp from job completion.
8. **SKUs:** no automatic SKU generation. Missing SKU = "needs fixing" (Tally items: use the item name as SKU only if unique within the file and not existing; otherwise needs fixing).
