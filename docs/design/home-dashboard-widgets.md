# Configurable screens: home dashboard widgets, module overviews and list tables

Status: design, 2026-11-25. Branch `feat/home-widgets`. Nothing is built yet.

The owner said: "dashboard is very boring, no charts, no drag or different positions I can arrange is not there",
then widened it: "for all screens it should be configurable and saved in user config."

This document designs one mechanism with three parts:

1. **A per-user, per-workspace UI config store** (`ui_config.*` commands, one table) and one React hook,
   `useScreenConfig`.
2. **Two building blocks that use it**: `WidgetGrid` (home and module overviews) and `ListTable` (list screens).
3. **A screen inventory and rollout order**, so builders can work in parallel.

## 1. What the code has today

| Area | Finding | File |
|---|---|---|
| Home | One command, `home.summary`, returns tiles and the attention list. Fixed layout: check-in card, ask card, big tiles, small tiles, attention list, snapshot. No charts. | `apps/web/src/components/home/dashboard.tsx`, `apps/api/internal/modules/home/home.go` |
| Charts | The report builder draws three things: `BigNumber`, `Bars` (CSS-width bars, each with its value as text) and a result table. There is no line chart and no sparkline. Spec `viz` is `table`, `bar` or `number`. | `apps/web/src/components/reports/result.tsx`, `apps/api/internal/modules/reports/spec.go` |
| Report dashboards | `core.dashboards` holds tiles `{report_id, size}`. `TileCard` runs one `report.run` per tile so a slow tile never blocks the rest. Reorder is by move buttons, not drag. | `apps/web/src/components/reports/dashboards.tsx`, `apps/api/internal/modules/reports/dashboards.go` |
| `report.run` | Takes a saved `id` or an ad-hoc `spec`. Finance datasets are hidden without finance access. Rate limit: burst 20, refill 2 per second, per user. | `apps/api/internal/modules/reports/reports.go` |
| Drag and drop | `@dnd-kit/core` 6.3.1 and `@dnd-kit/sortable` 10.0.0 are installed and used with `PointerSensor` and `KeyboardSensor`. Both are still the newest stable on npm. | `apps/web/src/components/crm/deals.tsx`, `projects/space-view.tsx` |
| Per-user settings | **None on the server.** `core.memberships` has role, finance access and branch only. The sidebar fold state lives in `localStorage`. | `migrations/0001_core.sql`, `apps/web/src/components/shell.tsx` |
| Tables | **No shared list table.** About 20 components hand-write `<table>`. Lists load up to 500 rows (`limit: 500`) and filter in the browser. No list has column choice or click-to-sort. | `components/billing/invoice-list.tsx`, `crm/contacts.tsx`, and others |
| UI kit | `btn`, `btnPrimary`, `inputCls`, `Field`, `Modal`, `Pill`, `EmptyState`, `ErrorText`. Tokens: `canvas`, `side`, `hover`, `line`, `fg`, `muted`, `accent`, `ok`, `warn`, `bad` (light and dark). | `apps/web/src/components/ui.tsx`, `apps/web/src/app/globals.css` |
| Boundaries | A module owns its tables. Modules talk through `c.Call` or events. No new module-to-module Go imports. | `docs/architecture/service-boundaries.md` |

## 2. Mobbin research

Searched on Mobbin on 2026-11-24. Odoo, Razorpay, Monday, ClickUp, Jira, Google Analytics, Amplitude dashboards in
edit mode and Zoho Books did not come up in the searches I ran, so they are not cited.

**Edit mode or always draggable.** Leading products use an explicit edit mode, because cards are also links.
- [Xero, Business Overview in edit mode](https://mobbin.com/flows/910a4138-8aad-4024-92f3-407ff4242bc4?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "Add widget" and "Done" at top right, a remove badge on each card, drag the whole card.
- [Stripe, Home "Your overview"](https://mobbin.com/screens/37e72894-2a2b-47f3-b85c-4c29270716a6?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "+ Add" and "Edit" buttons beside one shared date range.
- [Shopify, Analytics edit](https://mobbin.com/screens/abe020de-7640-469a-a000-6ec3f8bcc780?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): top bar "Unsaved changes, Cancel, Save", drag handles, "Reset to default", an Undo toast.
- [Sentry, dashboard edit](https://mobbin.com/flows/2b376f08-c0c8-4604-b093-14612926b263?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): handle, edit, duplicate and delete icons on every card; free resize.

**Widget gallery.**
- [Stripe, "Add charts"](https://mobbin.com/screens/d6088e69-df27-428d-bf1d-de2f0643f45a?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a modal of live previews with a tick on each, then "Apply".
- [Whop, "Select widgets"](https://mobbin.com/screens/9f348f8b-404e-4256-9486-20d8ffae430f?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): same pattern with a "10 widgets selected" count.
- [Hotjar, widget templates](https://mobbin.com/screens/b3360c74-40e2-496b-a05e-7ae4041c1d24?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): list on the left, preview and "Use this widget to…" on the right.
- [Zoho CRM, Customize Home page](https://mobbin.com/screens/bf94a083-b175-4415-8af3-95bdb876edd4?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a searchable component list in a side panel.

**Drag to reorder and resize.** Fixed sizes are the simpler pattern and suit a small-business owner.
- [Evernote, widget size](https://mobbin.com/flows/f5bc6876-ce26-43e0-9486-fa6b0d75d25f?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): three size buttons on each card (small, medium, large) and a "Drag widgets here" drop zone.
- [Salesforce, Lightning dashboard](https://mobbin.com/flows/55aead21-dd2b-4e1a-b4e6-430531505dce?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): free resize on a visible grid; powerful, but it needs handles on every edge.
- [Hotjar, moving a widget](https://mobbin.com/flows/f883e29b-b691-4082-ba92-048a03d6b6f7?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a shaded placeholder shows where the card will land.

**Removing and reset to default.**
- [Quicken, Customize Dashboard](https://mobbin.com/screens/6a522815-32c7-4369-b1af-408b76884daa?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): toggle list with move up and down arrows and "Reset to defaults".
- [Aboard, home edit](https://mobbin.com/screens/e903329c-269e-47fb-955f-52dd40c2b6b0?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a minus badge on each card and a floating "Add widget, Done" bar.
- [Evernote, Customize your Home](https://mobbin.com/screens/34eac6fa-2f24-40c8-a504-4b779fd8d7b2?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "Reset" above the available widgets.

**Per-widget settings.**
- [QuickBooks, home](https://mobbin.com/screens/30398513-946c-4aa2-b19b-8a4e6f34dfe3?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): each card has its own period menu ("Last month", "This financial year") and an "Add widgets" placeholder card.
- [Mixpanel, card menu](https://mobbin.com/screens/b2e7f096-7f25-4cdb-a80e-645ed595f5a1?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): Rename, Duplicate, Chart type, Delete in one "more" menu.
- [Hotjar, dashboard](https://mobbin.com/flows/f883e29b-b691-4082-ba92-048a03d6b6f7?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): one select in each card header.

**Empty states.**
- [Plane, empty dashboard](https://mobbin.com/screens/e47c670d-2734-4823-b844-35f2341c2ba9?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): one sentence and one "Add widget" button.
- [Supabase, "Add your first chart"](https://mobbin.com/screens/3dd361a6-4db4-4de0-9ab2-ea1e64c2123a?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a dashed drop area.
- [Whop, "No data available"](https://mobbin.com/screens/9f348f8b-404e-4256-9486-20d8ffae430f?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a widget with no data keeps its frame and says so.

**Mobile.** Phones use a list with handles and toggles, not a free grid.
- [Monzo, Edit spotlights](https://mobbin.com/screens/3e761fa7-4ca4-4aad-b980-fe36b36ec0f1?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): show or hide plus reorder rows, "Cancel" and "Done".
- [Rocket Money, Customize Dashboard](https://mobbin.com/screens/08e5646c-aa94-482f-ba8c-216eae2c71e3?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): grouped toggles with handles.
- [Garmin Connect, Edit At a Glance](https://mobbin.com/screens/660680ad-f2c3-482c-9dbb-cec487ad05bb?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a limit ("up to 8 stats") stated in the screen.

**KPI tiles and charts small businesses read easily.**
- [Shopify, Analytics](https://mobbin.com/screens/35153b56-4d7f-4398-ae6b-2fefaba5d024?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a row of small tiles (value plus sparkline), one line chart over time, horizontal bars for "sales by product".
- [Mercury, home](https://mobbin.com/screens/c6cc0102-adb2-4a1d-830c-ddc7f75fac62?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): value, "−122.5% vs prior period" in words, a trend line, category bars with percentages.
- [Xero, home](https://mobbin.com/screens/03585ce9-541c-4c11-9f37-421785ef2a11?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "Invoices owed to you" as ageing columns, with awaiting and overdue amounts above.
- [Stripe, outstanding invoices](https://mobbin.com/screens/d6088e69-df27-428d-bf1d-de2f0643f45a?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): one stacked bar and a legend with amounts for the ageing buckets.

Conclusion: lines for a trend over time, horizontal bars for a ranking, columns for ageing buckets. No pie chart is needed.

**Configurable tables.**
- [Shopify, Orders columns](https://mobbin.com/screens/23629390-45d4-47bf-b94e-d2a4fe43c030?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): one popover with sort, drag handles and an eye icon per column; changes apply at once. [Save view as](https://mobbin.com/screens/62d10f3a-1af9-4731-9d70-fca27b387369?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) names the current filters.
- [HubSpot, "Choose which columns you see"](https://mobbin.com/screens/906db1de-6503-4bfa-9a1d-671030000340?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): search on the left, ordered selection on the right, a "Frozen columns" count, the name column locked.
- [Linear, display options](https://mobbin.com/screens/815793b1-5c75-43ac-94c7-93380781e337?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): ordering, property chips, and "Reset" beside "Set default for everyone". [Saved view](https://mobbin.com/screens/ed670cda-0527-4716-a1a6-0159f12c4f42?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "Save to: Personal".
- [Notion, property visibility](https://mobbin.com/screens/98dde396-5c07-422d-a99c-885c83220249?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "Shown" and "Hidden" groups, handle and eye per row, the name row fixed.
- [Airtable, toggle column visibility](https://mobbin.com/screens/75be48f5-8187-4941-bf00-2d1e73e987ca?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a plain checkbox list.
- [Relevance AI, saved views as tabs](https://mobbin.com/screens/e56da5a0-b535-48e6-8901-19ae223f5d18?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): tabs with counts, "Save view" appears when filters change.

## 3. Decisions

| Question | Decision | Why |
|---|---|---|
| Edit mode or always draggable | **Edit mode** ("Customise") for widget grids. Table settings apply at once. | Tiles are links; dragging them in view mode would cause mis-clicks. Matches Xero, Stripe, Shopify. |
| Layout model | **An ordered list of widgets, each with a size.** No x/y coordinates. | An ordered list works on a phone, with a keyboard and with `@dnd-kit/sortable`. No grid library is needed. |
| Sizes | **S, M, L** = 1, 2 or 4 of 4 columns. Each widget type lists the sizes it allows. | Evernote pattern. Free resize needs handles and collision rules for little gain. |
| Chart library | **None.** Reuse `Bars` and `BigNumber`, and add one small file of inline SVG (`Sparkline`, `TrendChart`). | See section 7. |
| Table library | **None.** | Sort, hide and reorder over 500 loaded rows is about 150 lines. |
| Grid library | **None.** `@dnd-kit` is installed. | `react-grid-layout` 2.3.0 would add free placement nobody asked for. |
| Widget data | Existing commands, plus one new batched command, **`report.run_many`**. | Section 6. |
| Defaults | In web code, chosen by role. No row in the database means "default". | Reset is then a delete. New widgets reach people who never customised. |

Skipped on purpose: free-form placement, per-breakpoint layouts, several named home dashboards, sharing a layout
with a colleague, admin-set defaults for the whole workspace (see open questions), column width dragging.

## 4. The UI config store

### Data model: `migrations/1312_ui_config.sql`

```sql
-- One row per person, workspace and screen. No row = the screen's default.
CREATE TABLE core.ui_configs (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  screen       text NOT NULL CHECK (screen ~ '^[a-z][a-z0-9_.-]{0,63}$'),
  config       jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object' AND pg_column_size(config) <= 16384),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id, screen)
);
SELECT core.enable_tenant_rls('core.ui_configs');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.ui_configs TO cnote_app;
```

Every config is `{ "v": <integer>, ... }`. `v` is the schema version of that screen's config.

### Commands: new module `apps/api/internal/modules/uiconfig`

| Command | Group | Input | Output |
|---|---|---|---|
| `ui_config.get` | read | `{ screen }` | `{ config: object \| null, updated_at }` |
| `ui_config.set` | write | `{ screen, config }` | `{ updated_at }` |
| `ui_config.reset` | write | `{ screen }` | `{}` (deletes the row) |

Rules, all checked on the server:

- The caller must be a person (`Principal.UserID` set). API keys and agents get `ErrForbidden`. Guests may use it.
- `screen` matches the pattern above. `config` is a JSON object, at most 16 KB, with an integer `v` of 1 or more.
- At most 200 rows per person per workspace; `set` on a 201st screen returns `ErrInvalid`.
- Rate limit `set` with `command.CheckRateWith` (burst 30, 1 per second).
- The server does **not** know each screen's shape. The web validates the shape when it reads (below).
- A config never grants access. It only names widgets and columns; every data command still checks the caller.

Service boundary: the module owns `core.ui_configs`, imports no other module, and no other module reads the
table. It can move to its own service by routing the `ui_config.` prefix.

### Hook: `apps/web/src/lib/screen-config.ts`

```ts
function useScreenConfig<T extends { v: number }>(
  screen: string,
  defaults: T,
  parse: (raw: unknown, defaults: T) => T, // drops unknown ids, fills gaps, migrates older v
): { config: T; ready: boolean; customised: boolean; save: (next: T) => void; reset: () => void; error?: Error }
```

- Loads with `ui_config.get` once per screen and keeps the result in a module-level `Map` keyed by workspace
  and screen, so going back to a screen does not flash.
- `save` updates state at once (optimistic), then sends `ui_config.set` after a 500 ms pause. If the save fails
  the hook restores the last saved config and sets `error`; the screen shows it with `ErrorText`.
- `reset` sets `defaults` at once and calls `ui_config.reset`.
- `parse` treats stored JSON as untrusted. If stored `v` is higher than the code knows (an older tab), the hook
  uses defaults and does not overwrite the row.
- Until `ready`, the screen shows its existing skeleton.

Shortcut to record in code: two tabs editing the same screen are last-write-wins; add a revision check if people
report lost changes.

## 5. Building block one: `WidgetGrid`

Files: `apps/web/src/components/widgets/grid.tsx`, `gallery.tsx`, `types.ts`.

```ts
type WidgetDef = {
  type: string                       // 'kpi.sales_month'
  title: string
  group: 'Money' | 'Sales' | 'Work' | 'Website' | 'Lists' | 'Reports'
  sizes: ('s' | 'm' | 'l')[]         // first = default
  available: (ctx: { role; finance: boolean; modules: string[]; hasSite: boolean }) => boolean
  options?: { period?: string[] }    // per-widget settings offered in the card menu
  render: (props: { size; opts; data: WidgetData }) => ReactNode
  runs?: (opts) => Run[]             // report.run_many entries this widget needs
}
type GridConfig = { v: 1; widgets: { id: string; type: string; size: 's'|'m'|'l'; opts?: object }[] }

<WidgetGrid screen="home" catalog={homeWidgets} defaults={homeDefaults(role)} />
```

A screen supplies a catalog and defaults. The grid owns drag, resize, add, remove, reset and saving.

### View mode

```
 Home                              Tue 25 Nov      [All branches v]  [Customise]
+----------------+----------------+----------------+----------------+
| Sales, Nov     | To collect     | Cash in bank   | New leads      |
| Rs 4.2L        | Rs 7.8L        | Rs 12.4L       | 14             |
| ^ 12% vs       | Rs 2.1L        | 2 accounts     | ^ 3 vs last    |
|  1-25 Oct  /\/ | overdue (6)    |                |  week     _/\  |
+----------------+----------------+----------------+----------------+
| Sales trend, last 12 months     | Receivables ageing              |
|      _/\_      __/              | Not due   ######## 3.1L         |
|  __/     \___/                  | 1-30 d    #####    2.6L         |
| D J F M A M J J A S O N         | 31-60 d   ##       1.2L         |
| [View as table]                 | 60+ d     #        0.9L         |
+---------------------------------+---------------------------------+
| Needs your attention (7)                                          |
| ! Invoice INV-0042  Sharma Traders, overdue by 12 days   Rs 48,000|
| ! Task ENG-42 Fix GST rounding, overdue by 2 days                 |
+-------------------------------------------------------------------+
```

### Edit mode with the gallery

```
 Customise Home    Drag cards, or use the arrows.   [Reset to default] [Cancel] [Done]
+---------------------------------------------+ +----------------------------+
| [::] Sales, Nov      [S|M]  [^][v]  [x]     | | Add a widget   [Search...] |
|      Rs 4.2L                                | | Money                      |
+---------------------------------------------+ |  [+] Expenses by category  |
| [::] Sales trend     [M|L]  [^][v]  [...][x]| |  [+] Top customers         |
|      period: Last 12 months v               | | Sales                      |
+---------------------------------------------+ |  [+] Pipeline by stage     |
| [::] Needs attention [M|L]  [^][v]  [x]     | |  [ok] New leads   (added)  |
+---------------------------------------------+ | Website                    |
| . . . . drop here . . . .                   | |  [+] Website visits        |
+---------------------------------------------+ | Reports                    |
                                                |  [+] Pin a saved report... |
                                                +----------------------------+
```

- **Customise** opens edit mode. Changes are a draft; **Done** saves through `useScreenConfig`, **Cancel**
  discards. **Reset to default** asks once ("Put Home back to the standard layout?") using `Modal`.
- **Drag** by the handle `[::]`, using `DndContext`, `SortableContext` and `rectSortingStrategy` as the deals
  board does. A shaded placeholder shows the landing place.
- **Resize** with a segmented control that shows only the sizes the widget allows.
- **Remove** with `[x]`. It takes effect in the draft at once; Cancel undoes it.
- **Add** from the gallery: a side panel on desktop, a full-screen `Modal` on a phone. Widgets the person cannot
  use are not listed. Widgets already added show a tick; a widget can be added once, except "Pin a saved report".
- **Per-widget settings** sit in the card menu `[...]`: period for charts, report choice for pinned reports.
- **Limits**: at most 24 widgets (same as `maxTiles` in the report dashboards).
- **Empty**: with no widgets, show `EmptyState` with "Your home is empty" and an "Add a widget" button.
- **A widget whose module is switched off, or that the person lost access to**, is dropped by `parse` on read.
  A pinned report that was deleted shows "This report was removed" with a Remove button.

### Home widget catalog

| Widget | Sizes | Data | Who sees it |
|---|---|---|---|
| Sales this month (KPI, delta, sparkline) | S, M | `report.run_many`: invoices by month, sum of total, last 12 months; second run for the same days last month | finance |
| Receivables and overdue (KPI) | S, M | `home.summary` tile `payments_pending` | finance |
| Cash in bank (KPI) | S | `bank.account_list` (`balance_minor`) | finance |
| New leads (KPI, delta, sparkline) | S, M | `report.run_many`: deals by week on `created_on`, count | member, CRM on |
| Open deals (KPI) | S | `report.run_many`: deals not won or lost, count and sum of value | member, CRM on |
| Tasks due (KPI) | S | `home.summary` tile `tasks_due` | projects on |
| Website visits and enquiries (KPI, sparkline) | S, M | `site.list`, then `site.stats` (daily visits; enquiries in the last 30 days) | a site exists |
| Sales trend (line or columns) | M, L | shares the "Sales this month" run | finance |
| Receivables ageing (bars) | M, L | `report.receivables_ageing` | finance |
| Pipeline by stage (bars) | M, L | `report.run_many`: deals by stage, sum of value | member, CRM on |
| Expenses by category (bars) | M, L | `report.run_many`: expenses by category | member, billing on |
| Top customers (bars) | M, L | `report.run_many`: invoices by customer, top 5 | finance |
| Needs your attention (list) | M, L | `home.summary` `attention` | everyone |
| My tasks, Approvals, AI employee confirmations (lists) | M, L | `home.summary` `attention`, filtered by kind | as today |
| Check-in, Ask | M, L | existing `CheckInCard`, `AskCard` | as today |
| Pinned report | S, M, L | `report.run` by id, through the existing `TileCard` | report visibility |

The existing small tiles (follow-ups, bills due, tickets, low stock, expiring stock, people present, urgent)
stay as S widgets that read `home.summary`, so nothing a person sees today disappears.

Honest limits: receivables and cash are balances with no stored history, so those tiles show a detail line and
no sparkline. The sales delta compares month to date with the same days of last month and says so in words
("vs 1–25 Oct"); comparing 25 days with a full month would mislead.

**Finance access.** `available()` hides finance widgets in the gallery. The server is the real check:
`report.run` refuses finance datasets and `home.summary` omits money tiles. A widget that comes back denied
shows "Not available to you", as `TileCard` does now.

**Defaults per role** (`components/home/defaults.ts`):

- Owner or admin: receivables and overdue first (the number to act on), then sales, cash, new leads (S each); sales trend (M); receivables ageing (M);
  attention (L); pipeline by stage (M); approvals (M).
- Member with finance access: receivables, sales, tasks due, new leads; attention (L); my tasks (M); pipeline (M).
- Member without finance access: tasks due, new leads, open deals, follow-ups; attention (L); my tasks (M).
- Guest: tasks due; attention (L).

Widgets for modules that are off are filtered out of the defaults.

### Pin a saved report

The gallery's "Pin a saved report" opens the report picker already used in `DashboardView`. The widget stores
`opts: { report_id }` and renders with the exported `TileCard`. The report builder gains one button, "Pin to
Home", which appends that widget to the `home` config.

## 6. Widget data: commands

**Reuse**: `home.summary`, `report.run`, `report.receivables_ageing`, `bank.account_list`, `site.list`,
`site.stats`. Built-in chart specs are web-side constants, as report templates already are
(`components/reports/templates.ts`).

**New: `report.run_many`** in the `reports` module (it owns the compiler, so there is no new import).

```
in : { runs: [{ key, spec?, id?, period? }] }          // at most 12
out: { results: { [key]: { ok: true, result: RunResult } | { ok: false, code: 'denied'|'invalid'|'failed' } } }
```

- Each run goes through the same code path as `report.run`, with the caller's rights.
- One failed run does not fail the others.
- The whole batch costs one rate-limit token. Without this, a home with ten report widgets would use half of
  the `report.run` burst on every load.
- Read group, minimum role Member, like the other `report.*` commands.

**No change to `home.summary`.**

**Loading and caching** (`components/widgets/data.ts`):

- One `home.summary` call on load (as today).
- Widgets are split into "first eight" and "the rest". Each half sends one `report.run_many`. The second half
  is sent when its first card comes within 300 px of the viewport (`IntersectionObserver`).
- Results are cached in memory by workspace, widget type, options and branch for 60 seconds, and shown stale
  while a refresh runs.
- Every card has its own loading, denied and failed state. The skeleton is the same box as the loaded card
  (fixed sizes make this free), so nothing shifts when data arrives.
- The page header shows "Updated 2 min ago" and a Refresh button that clears the cache. No polling.
- Edit mode (drag code and gallery) loads with `next/dynamic`, so the normal read-only home does not ship it.

Shortcut to record in code: there is no server cache; add one in `report.run_many` if home load shows up in
slow-query logs.

## 7. Charts

**Decision: reuse the report builder's rendering and add about 120 lines of inline SVG. No new dependency.**

| Need | Component | Source |
|---|---|---|
| Ranking (top customers, expenses, pipeline) | `Bars` | exists in `reports/result.tsx` |
| Ageing buckets | `Bars` | exists |
| One number | `BigNumber` | exists; add optional `delta` and `spark` props |
| Sparkline | `Sparkline` | new, `components/charts/svg.tsx` |
| Trend over time, line or columns | `TrendChart` | new, same file |

`TrendChart` also improves the report builder: a `bar` report grouped by a date bucket draws as a trend over
time. That is a render rule only; the spec does not change.

Registry check on 2026-11-24, in case needs grow later:

| Package | Newest stable | Licence | React 19 | Unpacked size | Verdict |
|---|---|---|---|---|---|
| `recharts` | 3.11.1 | MIT | yes (peer range includes 19) | 8.9 MB | The fallback if we later need stacked series, tooltips or zoom. |
| `chart.js` | 4.6.2 | MIT | needs a wrapper | 6.2 MB | Canvas; weaker for screen readers. |
| `uplot` | 1.6.32 | MIT | no React binding | 0.5 MB | Canvas; last release March 2025. |

Two chart shapes do not justify a library. Inline SVG uses the app's tokens directly (`accent`, `muted`, `line`),
works in light and dark, and has no client bundle cost.

## 8. Building block two: `ListTable`

There is no shared list table to extend. The only shared table, `DataTable` in `reports/result.tsx`, renders a
report result and stays as it is. The smallest path is one new component that list screens adopt one by one,
replacing their hand-written `<table>` and keeping their own data loading and filters.

File: `apps/web/src/components/list-table.tsx`.

```ts
type Column<T> = {
  key: string; label: string
  cell: (row: T) => ReactNode
  sort?: (row: T) => string | number | null   // present = sortable
  align?: 'right'; locked?: boolean           // locked = always first and visible (the name or number)
  hidden?: boolean                            // hidden by default
  finance?: boolean                           // dropped when the person has no finance access
}
type TableConfig = {
  v: 1; columns: string[]                      // visible keys, in order
  sort: { key: string; dir: 'asc' | 'desc' } | null
  density: 'compact' | 'standard' | 'relaxed'; pageSize: 25 | 50 | 100; freezeFirst: boolean
  views: { id: string; name: string; filters: object }[]   // at most 10
}

<ListTable screen="billing.invoices" columns={cols} rows={rows} rowHref={...}
           filters={filters} onFilters={setFilters} empty={...} />
```

```
 Invoices  [All] [Overdue] [Sharma, unpaid] [+ Save view]     [Search]  [Columns v]
 +-----------+---------------+----------+--------+-----------+      +---------------------+
 | Number ^  | Customer      | Due      | Status | Outstand. |      | Columns             |
 | INV-0042  | Sharma Trad.  | 13 Nov   | Sent   |  48,000   |      | [=] Number  (fixed) |
 | INV-0043  | Gupta & Sons  | 20 Nov   | Paid   |       0   |      | [=] [x] Customer ^v |
 +-----------+---------------+----------+--------+-----------+      | [=] [x] Due      ^v |
 Rows per page [50 v]   1-50 of 312   [<] [>]                       | [=] [ ] GSTIN    ^v |
                                                                    | Density ( ) Compact (o) Standard ( ) Relaxed |
                                                                    | [x] Keep first column in place      |
                                                                    | Reset to default    |
                                                                    +---------------------+
```

- **Columns popover**: checkbox to show or hide, drag handle and up/down buttons to reorder, density
  (row height 32, 40 or 48 px; 40 is the default), freeze first column, "Reset to default". Changes apply and save at once (Shopify and Linear pattern).
- **Sort**: click a header; `aria-sort` on the `<th>`; a header is a `<button>`. Sort is saved per screen, not
  per saved view (Shopify does the same), so switching views never reorders rows unexpectedly.
- **Semantics**: a native `<table>`, not `role="grid"`. The rows hold links and a menu, not editable cells.
- **Saved filters**: the screen passes its filter object and a setter. "Save view" stores the current filters
  under a name; views show as tabs. The layer does not interpret the filters.
- **Paging** is in the browser over the rows already loaded.
- **Frozen first column**: `position: sticky; left: 0` with the `canvas` background.
- **Phone**: the table scrolls sideways inside its container with the first column frozen; the popover becomes a
  full-screen `Modal`.

Shortcut to record in code: sorting and paging cover only the rows the screen loaded (500 today); move to
server-side sort and cursor paging when a list command supports it.

## 9. Accessibility

- **Drag has a non-drag alternative** (WCAG 2.2, 2.5.7): Move up and Move down buttons on every widget and
  every column row, at least 24 by 24 px. On a phone these are the main way to reorder.
- **Keyboard drag**: `KeyboardSensor` with `sortableKeyboardCoordinates`. Space lifts, arrows move, Space drops,
  Escape cancels. The handle is a `<button>` labelled "Move Sales this month".
- **Announcements**: pass `accessibility.announcements` to `DndContext`: "Picked up Sales this month, position
  1 of 9", "Moved to position 3 of 9", "Dropped". The up/down buttons, add, remove and resize announce through
  one `aria-live="polite"` region.
- **Edit mode**: focus moves to the "Customise Home" heading on entry and back to the Customise button on exit.
- **Charts**: each chart is `role="img"` with an `aria-label` that states the result ("Sales, last 12 months.
  Highest Rs 6.1L in October, latest Rs 4.2L in November"), plus a "View as table" toggle that shows the same
  numbers in a real table. Sparklines are `aria-hidden`; the tile's label carries the value and delta.
- **Never colour alone**: deltas show an arrow and words ("up 12%"); overdue keeps its icon and text.
- **Motion**: no drag animation when `prefers-reduced-motion` is set.

## 10. Mobile

- One column; sizes are ignored below the `sm` breakpoint. Order is the same as on desktop.
- Edit mode becomes a list of rows (title, move up, move down, remove) as in Monzo and Rocket Money. Drag is
  off on touch screens to avoid fighting page scroll.
- The gallery and the columns popover open as a full-screen `Modal`.
- Layouts are not stored per device. One order serves both.

## 11. Screen inventory

Routes under `apps/web/src/app/w/[ws]/`. Block: **G** = `WidgetGrid`, **T** = `ListTable`, **–** = no change.

| Route | Component | Block | Screen key |
|---|---|---|---|
| `/` | `HomeDashboard` | G | `home` |
| `/billing` | `InvoiceList` | T | `billing.invoices` |
| `/billing/quotes` | `QuoteList` | T | `billing.quotes` |
| `/billing/bills` | `BillList` | T | `billing.bills` |
| `/billing/expenses` | `ExpenseList` | T | `billing.expenses` |
| `/billing/stock` | `StockList` | T | `billing.stock` |
| `/billing/stock/expiry` | `ExpiryReport` | T | `billing.stock_expiry` |
| `/billing/recurring` | `RecurringList` | T | `billing.recurring` |
| `/billing/bank` | `BankReconcile` | T (lines table only) | `billing.bank_lines` |
| `/billing/reports` | `Reports` | – (fixed statutory reports) | |
| `/crm` | `ContactsList` | T | `crm.contacts` |
| `/crm/tickets` | `TicketList` | T | `crm.tickets` |
| `/crm/follow-ups` | `FollowUps` | T | `crm.follow_ups` |
| `/crm/deals` | `Board` | – (kanban; already drag) | |
| `/crm/inbox` | `Inbox` | – (conversation list) | |
| `/people` | `PeopleDirectory` | T | `people.directory` |
| `/people/attendance` | `Attendance` | T | `people.attendance` |
| `/people/leave` | `LeavePage` | T | `people.leave` |
| `/people/payroll`, `/people/payslips` | `PayrollHome`, `Payslips` | T | `people.payroll`, `people.payslips` |
| `/projects` | `ProjectsHome` | G (later) | `projects.overview` |
| `/projects/[key]` | `SpaceView` | – (board and list have their own view state) | |
| `/projects/triage` | `TriageView` | T (later) | `projects.triage` |
| `/reports` | `Library` | T | `reports.library` |
| `/reports/dashboards/[id]` | `DashboardView` | – now; could adopt G later | |
| `/approvals` | `ApprovalInbox` | T (later) | `approvals.inbox` |
| `/site` | `SiteHome` | G (later) | `site.overview` |
| `/docs`, `/s/[id]`, `/search` | page lists | – | |
| `/chat`, `/whiteboard`, `/n/[id]`, `/marketplace`, `/import/*`, `/settings`, all `[id]` detail pages, settings pages | | – | |

**Module overviews.** CRM, Billing and People have no overview screen today; their landing page is a list.
Proposal: a collapsible `WidgetGrid` strip above the list on each module landing page (`crm.overview`,
`billing.overview`, `people.overview`), with a catalog filtered from the home catalog by group. No new routes.

### Rollout order

1. **Foundation and Home**: store, hook, `WidgetGrid`, charts, `report.run_many`, new home.
2. **Six list screens**: invoices, contacts, quotes, bills, expenses, stock. I found no usage data in the repo,
   so this order is a judgement from the core Lead to Payment journey; confirm it from audit-log counts.
3. **Module overviews**: billing, CRM, projects, people, website.
4. **Remaining lists**: tickets, follow-ups, people, attendance, leave, payroll, recurring, expiry, report library.

## 12. Work packages

Files are disjoint inside a wave, so builders can run in parallel. Wave 2 starts when wave 1 is merged.

**Wave 1**

| WP | Scope | Files (only these) |
|---|---|---|
| A. Go: config store | Migration, three commands, registration, tests | `migrations/1312_ui_config.sql`, `apps/api/internal/modules/uiconfig/*` (new), `apps/api/internal/modules/modules.go`, `apps/api/internal/modules/mcpmeta.go`, `apps/api/internal/server/uiconfig_test.go` (new) |
| B. Go: batched runs | `report.run_many` and tests | `apps/api/internal/modules/reports/reports.go`, `apps/api/internal/modules/reports/run_many.go` (new), `apps/api/internal/server/reports_test.go` |
| C. Web: hook and grid | `useScreenConfig`, `WidgetGrid`, gallery, types | `apps/web/src/lib/screen-config.ts` (new), `apps/web/src/components/widgets/{grid,gallery,types}.tsx` (new) |
| D. Web: charts | `Sparkline`, `TrendChart`, `BigNumber` delta and spark, date-bucket rule for `bar` | `apps/web/src/components/charts/svg.tsx` (new), `apps/web/src/components/reports/result.tsx` |
| E. Web: table | `ListTable` and its columns popover | `apps/web/src/components/list-table.tsx` (new) |

C, D and E agree on two contracts before starting: `WidgetDef` and `GridConfig` (section 5) and
`ui_config.*` (section 4). C and E mock the commands until A lands.

**Wave 2**

| WP | Scope | Files |
|---|---|---|
| F. Home | Catalog, defaults, data loader; split `dashboard.tsx` into widgets; "Pin to Home" | `apps/web/src/components/home/*`, `apps/web/src/components/widgets/data.ts` (new), `apps/web/src/components/reports/dashboards.tsx` (export `TileCard`), `apps/web/src/components/reports/builder.tsx` (one button) |
| G1 to G6. Lists | One builder per screen | `billing/invoice-list.tsx`; `crm/contacts.tsx`; `billing/quotes.tsx`; `billing/bills.tsx`; `billing/expenses.tsx`; `billing/stock.tsx` |
| H. End-to-end | Playwright specs | `e2e/home.spec.ts`, `e2e/home-widgets.spec.ts` (new), `e2e/list-table.spec.ts` (new) |

**Wave 3**: module overviews (one WP per module landing page) and the remaining lists.

Every WP runs `pnpm lint` at the repo root before it is handed back, and `graphify update .` after code changes.

## 13. Tests

**Go (integration, `apps/api/internal/server`)**
- `ui_config`: set then get returns the same config; reset returns null; another person in the same workspace
  gets null; the same person in another workspace gets null; an API key or agent is refused; a config over
  16 KB, a non-object, a missing `v` and a bad screen key are each refused; the 201st screen is refused.
- `report.run_many`: a member without finance access gets `denied` for an invoices run and `ok` for a tasks
  run in the same batch; 13 runs are refused; a guest is refused; one batch uses one rate-limit token.

**Web unit** (pure functions, no new test tooling if none exists; otherwise assert-based checks)
- `parse` for grid config: drops unknown widget types, clamps a size the widget does not allow, removes
  duplicate ids, caps at 24, returns defaults for a higher `v`.
- `parse` for table config: drops unknown columns, keeps the locked column first, falls back on a bad sort key.
- `TrendChart` path maths: empty series, one point, all zeros, negative values.

**Playwright**
- Home: Customise, drag "Sales trend" above "Sales this month" with the keyboard (Space, arrow, Space), Done,
  reload; the order holds. Same with the Move up button.
- Home: add "Pipeline by stage" from the gallery, resize to L, remove a widget, reload; then Reset to default.
- Home: a member without finance access sees no money widgets in the page or the gallery.
- Home: pin a saved report from the report builder and see it on Home.
- Home at 390 px wide: one column, reorder with the Move down button.
- Invoices: hide a column, reorder, sort by Due, switch to compact, reload; the settings hold. Save a view,
  open it from its tab. Reset to default.
- The existing `e2e/home.spec.ts` must keep passing: overdue task, unpaid invoice and agent confirmation still
  appear and link through.
- Axe check on Home in view mode and edit mode.

## 14. Open questions

1. **Workspace defaults.** Should an owner be able to set the starting layout for everyone ("Set default for
   everyone", as Linear does)? It is one nullable `user_id` away, but nobody has asked for it yet.
2. **Top six lists.** Confirm the order from real usage; the list in section 11 is a judgement.
3. **Cash in bank.** `balance_minor` is the last imported statement balance. Is a stale balance acceptable on
   Home if the tile shows its "as of" date?
4. **One period for the whole page.** The design gives each chart its own period menu (QuickBooks pattern).
   The web research ranks a single page-level range with Indian presets higher. The report dashboards already
   have a page-level period select; should Home get the same in the first release?
5. **Branch filter.** Today only invoice tiles follow the branch selector. Should chart widgets follow it too?
   The invoices dataset would need a branch field in the report registry.
6. **Agents.** `ui_config.*` is refused for agents. Should an AI employee be allowed to arrange a person's Home
   on request ("put sales first")?
7. **Report dashboards.** Move `/reports/dashboards/[id]` onto `WidgetGrid` to get drag there too, or leave it?

## 15. Web research report: what it changed

`docs/design/dashboard-research.md` (web sources) was read after the first draft. Its ten recommendations and
this design agree on: explicit edit mode, fixed size steps, an ordered list on a CSS grid with the installed
`@dnd-kit` packages, a mandatory non-drag path, one versioned config per person per screen validated on load,
role defaults with reset, bars and lines only, hand-written SVG for sparklines, per-widget loading and error
states, and a single column on phones.

Changes made because of it:

- Owner default now starts with overdue receivables (action first, top left).
- Tables have three densities (32, 40, 48 px) instead of two, and sort is stated as per screen, not per view.
- Native `<table>` semantics are stated explicitly.
- "Updated N min ago" with a manual Refresh, same-size skeletons, and lazy-loaded edit mode were added.
- The page-level period is raised as open question 4.

Where this design differs, and why:

| Research recommends | This design | Reason |
|---|---|---|
| `@tanstack/react-table` 9.3.1 for tables | No library in the first release | Lists already hold up to 500 rows in the browser and need only hide, reorder, sort and a sticky first column. Adopt TanStack Table when a list needs column resizing, several pinned columns or server-side sorting; `TableConfig` already matches its state shape. |
| Server pagination at 25 to 50 rows | Browser paging over loaded rows | The list commands have no cursor today. Recorded as a shortcut in section 8. |
| `<Suspense>` per widget and Next `use cache` | Client fetch with a 60-second memory cache | Every screen here is a client component calling the Go API through `useCmd`; tenant and permission checks live in Go. Moving widget data to Server Components is a separate change. |
| Saved views shareable to the team | Private only | Sharing needs ownership and permission rules; see open question 1. |
| Four-colour categorical palette | Not needed yet | Every chart in the catalog is one series in `accent`. Use that palette if a multi-series widget is added. |
| Recharts, lazily, when a chart needs axes and tooltips | Same | `TrendChart` has a light axis and no tooltip; values are in the "View as table" toggle. |

## 16. Binding revisions after design review (supersede earlier sections)

**Staged delivery.** Release 1 (this build): the per-user config store, `useScreenConfig`, `WidgetGrid`, the configurable Home behind `HOME_WIDGETS=1` (the old Home stays the default until the flag is on; all existing Home actions and deep links are preserved as widgets), the SVG sparkline/trend charts and `report.run_many`. Release 2 (next PR): `ListTable` and the list-screen rollout, one screen at a time, each documenting its real data source (server filters, caps, URL params) before conversion. Module overview strips come after Release 2.

1. Config ownership: commands accept only `Principal.Kind == KindUser` with a non-nil user id; workspace and user always come from the principal and appear in every predicate.
2. Limits: request body ≤ 32 KB before decoding; serialized config ≤ 16 KB; nesting depth ≤ 6; strings ≤ 200 chars; arrays ≤ 100 items; screen keys `^[a-z0-9_.:-]{1,64}$`; at most 200 rows per user per workspace enforced atomically (insert ... where (select count) < 200 under an advisory lock on workspace+user).
3. `report.run_many` charges every attempted run against the same bucket as `report.run` (no batch discount) and refuses the whole batch if the bucket cannot cover it; ≤ 12 runs and ≤ 256 KB response per call.
4. Each run in a batch executes through the same path as `report.run` (visibility, readable spaces, module state, finance at dataset and field level, period validation, limits, timeouts) inside its own savepoint, so one failure cannot abort the others.
5. Client caches (config and widget data) are keyed by user id, workspace, role and finance flag, and cleared on logout and workspace switch.
6. Finance: every widget lists the exact command it calls; finance access is enforced by those commands on the server (the gallery hiding is cosmetic). Deal values and expense amounts are finance-only.
7. Widget semantics match existing ones: "new enquiries" uses `home.summary`'s definition; the branch selector applies only to invoice-based widgets, labelled as such.
8. Batching chunks by run count (≤ 12), deduplicates identical runs, and each widget renders as soon as its own runs finish (cards never wait for an unrelated slow run).
9. `useScreenConfig` keeps a per-screen write sequence: a newer save supersedes older pending ones, reset cancels pending saves, late responses never overwrite newer state, unmount flushes the pending save, and save/reset are awaitable with a visible error state.
10. Stored config is never pruned because a widget is temporarily unavailable: the renderer shows the renderable projection and keeps unavailable widgets in storage; only the owner's explicit remove deletes one. Schema migrations add new default widgets only for configs older than the version that introduced them.
11. Accessibility acceptance: focus moves to a sensible target after add, remove, reorder, reset, closing the gallery and a failed save; size controls are radio groups with selected state; move buttons disable at the edges; every change and failure is announced via a live region; tested by keyboard-only e2e.
12–13. List screens (Release 2) get a typed per-screen contract (filters serialization, sort semantics for dates, money, nulls and ties, pagination reset rules, URL deep links) written before each conversion; no partial client-side result is presented as complete.
14. Migration **1312**; the new `uiconfig` module registers its commands with metadata and roles, imports no other module, and is added to the boundary test.
15. Release 1 is behind `HOME_WIDGETS=1`, with e2e covering owner, member without finance, and mobile; flipping the flag off restores the old Home.

## 17. Release 2 screen contracts

First slice: the invoices list and the contacts list, on the shared `ListTable`
(`apps/web/src/components/list-table/`). Written before conversion, per 16.12 and 16.13.

### 17.1 Decisions for `ListTable`

- **No TanStack Table.** The newest stable is `@tanstack/react-table` 9.3.1 (npm `latest`; alpha and beta tags are
  ignored). What we need is a column list, visible order, one sort key and a page slice: about 100 lines in
  `config.ts`. Column pinning is CSS (`position: sticky`), and we use no grouping, resizing, filtering or virtual rows.
  v9 is a new major line, and adopting it for this would add about 31 KB gzip and a migration surface for no gain.
  Revisit when a screen needs server-driven sorting with row models, resizing or virtualisation.
- **Native `<table>`**, `<th scope="col">`, `aria-sort` on the sorted header, header buttons only on sortable columns,
  a visually hidden `<caption>` with the row count. No `role="grid"`.
- **Settings in one `Modal`** (native `<dialog>`: focus trap, Esc, focus returns to the Columns button), on desktop and
  phone alike. This replaces the popover in section 8 to keep one code path.
- **Reorder** uses the already installed `@dnd-kit` with `KeyboardSensor` and announcements, plus Move up and Move down
  buttons (disabled at the edges; focus moves to the other arrow when one is disabled). Show and hide are checkboxes;
  hidden columns sit in their own list. The first column is locked: always first, always shown.
- **Mobile: horizontal scroll with the first column frozen**, not cards. Both screens are wide, column settings apply
  unchanged, table semantics stay, and cards would need a second template per screen. The scroll region is focusable
  and labelled. "Keep the first column in place" turns the freeze off.
- **Three densities**: row height 32, 40 (default) and 48 px.
- **Stored shape** (`ui_config`, screens `billing.invoices` and `crm.contacts`): `{v:1, columns, sort, density,
  pageSize, freezeFirst, views}`. Parsed defensively: unknown columns dropped, enums clamped, at most 10 views, names
  at most 40 characters, filter values at most 200. A newer `v` shows the default and disables editing.
- **Sort is saved per screen, not per view.** A view stores filters and visible columns. Applying a view sets both.
  "All" clears the filters. A view shows as pressed when its filters equal the current ones.
- **Reset to default** (confirmed) deletes the stored row: columns, order, sort, density, page size, freeze and views.
- **Announcements**: one polite live region in the table (sort, page, view saved, applied, deleted, page size) and one in the
  dialog (hide, show, move, density, freeze, reset). Save failures render as `role="alert"`.

### 17.2 Sort and paging semantics (both screens)

- A sort cycle on a header is ascending, descending, then off. Off means the server order.
- Null and empty values sort last in both directions. Numbers (money in minor units) compare numerically. Text
  compares numeric-aware and case-insensitively (`INV-2` before `INV-10`). ISO dates (`YYYY-MM-DD`) compare as text.
  Ties keep the server order in both directions.
- Paging is in the browser over the rows loaded. Page size is 25, 50 (default) or 100.
- The page returns to 1 when the sort, the page size, any filter, or the loaded row count changes. The page number is
  not in the URL.
- **Neither command pages by cursor or sorts by column**, and both cap at 500 rows. When the loaded set reaches 500
  the table shows "Showing only the ... Sorting and paging cover these rows, not every record." We never show a
  capped window as complete. Moving to server sort and paging needs `order_by`/`cursor` inputs on the commands; that is out of scope here.

### 17.3 Contract: invoices (`/w/[ws]/billing`, screen `billing.invoices`)

| Item | Contract |
|---|---|
| Source | `invoice.list`. Server inputs used: `limit` (500, the server maximum) and `branch_id`. The command also accepts `status`, `contact_id` and `kind`; the screen does not use them. |
| Server order | Newest first (`created_at desc`). This is the "off" sort. |
| Cap | 500. Beyond it the table says it shows the newest 500. |
| Client-side filters | Search box (number, customer, status, total) and the `filter=outstanding|overdue` deep link. Both run over the loaded 500 only, as before. The "partial" note appears when 500 were loaded. |
| Customer names | From `contact.list` limit 500; a customer beyond that shows a blank name (existing behaviour). |
| URL | `?filter=outstanding` and `?filter=overdue` (Home tiles link here); any other value is ignored. Unchanged. "Show all" in the banner links to `/billing`. Applying a view or "All" rewrites `filter` with `router.replace`. |
| Filters object (for views) | `{q, branch, filter}`, all strings, empty means off. |
| Actions kept | New invoice (modal, branch picker, create draft, redirect), number link to the invoice, the banner link, empty states (no space, no invoices, no match), branch selector, search. |
| Columns | Number (locked, text), Customer (text), Date, Due (ISO, null last), Status (label text), Total and Outstanding (minor units; Outstanding is null for drafts, cancelled and credit notes, so those sort last); hidden by default: Paid, Buyer GSTIN. |
| Default sort | None (server order). |

### 17.4 Contract: contacts (`/w/[ws]/crm`, screen `crm.contacts`)

| Item | Contract |
|---|---|
| Source | `contact.list`. Server inputs used: `q` (name, email or company, case-insensitive substring) and `limit` 500. |
| Server order | Name ascending. This is the "off" sort. |
| Cap | 500. The note reads "first 500 contacts by name". |
| Search | Server-side through `q`, so a search is complete within the cap. Changing it reloads and returns to page 1. |
| URL | None, today or after this change. |
| Filters object (for views) | `{q}`. |
| Actions kept | New contact (modal), Import link, name link to the contact page, empty states, no-space prompt, search. |
| Columns | Name (locked), Type, Email, GSTIN, State (text, by state name); hidden by default: Phone, Company. Null and empty last. |
| Default sort | None (server name order). |

### 17.5 Not done in this slice

Sticky header row, server-side sort and paging, shared (team) views, per-view sort, and the other list screens.
