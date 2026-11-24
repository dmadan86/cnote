# Dashboard research: how the best dashboards are built

Research date: 2026-11-24/25. Web sources only (not Mobbin). Feeds the dashboard design doc.

Verification key: **[V]** = read from the linked page or the npm registry during this research. **[S]** = secondary summary of the primary source. **[U]** = unverified here (from general knowledge, or the page could not be fetched); check before relying on it.

---

## 1. Principles

### What a dashboard is for

- Few: a dashboard is a single-screen display of the most important information needed to do a job. His first pitfall is "exceeding the boundaries of a single screen": scrolling or switching screens forces people to hold values in short-term memory. [S] ([summary part 1](https://www.thedataschool.co.uk/anh-vu/are-you-making-these-13-dashboard-design-mistakes/), [part 2](https://www.thedataschool.co.uk/anh-vu/are-you-making-these-13-dashboard-design-mistakes-part-2); original essay [Common Pitfalls PDF](https://www.perceptualedge.com/articles/Whitepapers/Common_Pitfalls.pdf), downloaded but not machine-readable here)
- NN/g: dashboards are for fast consumption with minimal interaction, not exploration. Operational dashboards (changing data, time-sensitive action) and analytical dashboards (prompt deeper analysis) both need a single-screen, at-a-glance view. [V] ([NN/g](https://www.nngroup.com/articles/dashboards-preattentive/))
- Carbon separates presentation dashboards (status of KPIs) from exploration dashboards (filter, drill). A small-business home screen is the first kind. [V] ([Carbon](https://carbondesignsystem.com/data-visualization/dashboards))

### Layout and hierarchy

- Most important content at the top, read left to right then top to bottom; give it the highest contrast and largest area; end with the least important. [V] (Carbon, same page). "Top-left = the number the owner acts on first" is the practical reading of this.
- "Limit the number of metrics"; show non-essential information only on demand. Carbon gives no number. [V] The common "4 to 6 KPI tiles" figure is a convention, not from a source read here. [U]
- Few's other pitfalls that matter for us: inadequate context for a number, excessive detail or precision, deficient measures, meaningless variety, poor arrangement, misused colour, zero-baseline violations. [S]

### KPI tiles: context, deltas, sparklines

- A number without context is Few's pitfall 2: pair every KPI with a comparison or trend. [S]
- Tufte: a sparkline is "a small intense, simple, word-sized graphic with typographic resolution" that can sit anywhere a number can. [V] ([Tufte](https://www.edwardtufte.com/notebook/sparkline-theory-and-practice-edward-tufte/)) So: no axes, no legend, no tooltip needed; the number beside it carries the value.
- Salesforce SLDS: the metric tile is the most common format; do not decorate it; a time series adds range, rate of change and pattern. [S] (search summary of [SLDS metric display](https://lightningdesignsystem.com/guidelines/data-visualization/metric-display); the page itself did not render)
- Tufte's data-ink ratio (remove ink that carries no data) is from *The Visual Display of Quantitative Information*. [U]

### Chart choice for non-analysts

- Use length and 2D position for quantities: bars, lines, scatter. Sort bars. [V] (NN/g)
- Avoid pie/donut except for one overwhelming share; avoid gauges (bullet charts are better), treemaps, and all 3D. [V] (NN/g)
- UK Government Analysis Function: line for time series, bars for ranking and magnitude; bar axes must start at zero; no dual axes; maximum four lines, four bars per cluster, four segments per stack; pie only with five or fewer categories and one dominant; at most ten light-grey gridlines; label directly instead of using a legend; horizontal text only. [V] ([AF charts guidance](https://analysisfunction.civilservice.gov.uk/policy-store/data-visualisation-charts))
- Title test: if you cannot state the chart's message in a few sentences, simplify. Use a headline title that states the message plus a statistical subtitle (what, where, when). [V] (same)

### Colour and accessibility

- Do not use colour to show magnitude, and never colour alone for category: add shape, grouping or labels. [V] (NN/g). Atlassian says the same and adds: do not use more than one colour unless it communicates something; keep separate tokens for categorical series and for status (success, warning, danger). [S] ([Atlassian](https://atlassian.design/foundations/color/data-visualization-color))
- WCAG numbers: graphical parts need 3:1 against adjacent colours (1.4.11); text needs 4.5:1; colour must not be the only means (1.4.1). [V] ([AF colours](https://analysisfunction.civilservice.gov.uk/policy-store/data-visualisation-colours-in-charts))
- A tested colour-blind-safe categorical palette, in order: `#12436D`, `#28A197`, `#801650`, `#F46A25` (plus `#3D3D3D`, `#A285D1` only when essential). Each is 3:1 on white and against its neighbour. Limit of four categories is best practice. [V] (same)
- Red/amber/green cannot be made to contrast with each other and with white; always pair status colour with text or an icon. [V] (same). For deltas this means an arrow and a sign, not just green/red.
- Every chart needs a text alternative: a short description of the takeaway, a data table, or both. [V] (AF charts)
- Stripe built its palette in a perceptually uniform colour space so contrast between steps is predictable (4.5:1 small text, 3:1 large). [V] ([Stripe](https://stripe.com/blog/accessible-color-systems)). Linear's redesign aimed to "reduce visual noise, maintain visual alignment, and increase the hierarchy and density". [V] ([Linear](https://linear.app/now/how-we-redesigned-the-linear-ui))

### Number formatting (India)

- Tested on Node 26.10: `Intl.NumberFormat('en-IN', {style:'currency', currency:'INR'})` gives `₹12,34,567.50`. With `notation:'compact'`: `₹1.2Cr`, `2.5L`, `₹45K`. `hi-IN` gives `2.5 लाख`. [V] (local run). No library or hand-rolled lakh/crore code is needed. Browser ICU output can differ slightly between engines; snapshot-test it. [U]
- Rule from Few: no false precision. Tiles show `₹1.2Cr`; the exact figure goes in the tooltip, title attribute and table. [S]
- Right-align numbers in tables and axes so digits line up. [V] (AF charts)

### States, density, time ranges, mobile

- Empty: Carbon has separate empty-state layouts for small tiles and larger areas. [S] ([Carbon empty states](https://carbondesignsystem.com/patterns/empty-states-pattern/)). An empty widget should say what will appear and link to the action that fills it. [U]
- Loading: Next.js recommends "meaningful" loading states such as skeletons shaped like the final content. [V] ([Next.js](https://nextjs.org/docs/app/getting-started/fetching-data))
- Error: fail per widget, never the page; show a retry and the last-updated time. [U]
- Density: Carbon data-table rows are 24/32/40/48/64 px; 40 is the default, 64 only for two-line rows. [V] ([Carbon data table](https://carbondesignsystem.com/components/data-table/usage/))
- Time ranges: Linear dashboards have dashboard-level filters applied to every insight plus per-insight filters. [V] ([Linear dashboards](https://linear.app/docs/dashboards)). One global range with "vs previous period" as the default comparison is the usual pattern; Indian financial year (April to March) and GST filing periods should be presets. [U]
- Vanity metrics: Few's "deficient measure". Each tile should answer "what do I do about this?" (overdue receivables, not total invoices ever raised). [S]/[U]
- Mobile: no source read here gives dashboard-specific mobile rules. Working assumption: single column, same widget order, no drag on touch by default. [U]

---

## 2. Customisable dashboards

- **Edit mode, not always-on drag.** Always-on drag steals clicks and scroll gestures from the content. No authoritative source was read that states this as a rule; it is the pattern used by Grafana, Datadog and Jira dashboards. [U]
- **Fixed size steps, not free resize.** react-grid-layout models this directly: named breakpoints (lg 1200, md 996, sm 768, xs 480) each with a column count and its own layout. [V] ([RGL README](https://github.com/react-grid-layout/react-grid-layout)). Offering a few sizes per widget (for example 1x1, 2x1, 2x2) keeps every chart legible and removes most resize bugs. [U]
- **Dragging must have a non-drag alternative.** WCAG 2.2 SC 2.5.7 (AA): "All functionality that uses a dragging movement for operation can be achieved by a single pointer without dragging." [V] ([W3C](https://www.w3.org/WAI/WCAG22/Understanding/dragging-movements.html)). NN/g says the same for column reorder. [V] So each widget needs "Move up / Move down / Resize / Remove" in a menu.
- **Keyboard and screen reader.** `@dnd-kit/core` ships a keyboard sensor (Space/Enter to pick up and drop, arrows to move, Escape to cancel), `aria-roledescription="draggable"`, hidden instructions, and a live region for start/over/end/cancel. The docs say to treat the defaults as starting points, localise the strings (English only), and announce 1-based positions. [V] ([dnd-kit accessibility](https://dndkit.com/guides/accessibility)). The react-grid-layout README documents mouse and touch only; no keyboard support is documented. [V]
- **Ownership and sharing.** Linear dashboards have an owner and live at personal, team or workspace scope. [V] HubSpot views are private by default, shareable to team or everyone. [V] ([HubSpot](https://knowledge.hubspot.com/records/create-and-manage-saved-views))
- **Persistence.** RGL warns saved layouts may be stale and normalises them with `validateLayoutInput`. [V] Apply the same idea: store `{version, widgets:[{id, type, size, order, config}]}` per user per screen; on load, drop unknown widget types, clamp sizes, append new default widgets, and migrate by version. Save on "Done", not on every drag. [U]
- **Gallery, reset, role defaults.** Widget gallery with a live preview, "Reset to default" with undo, and a different default layout per role (owner, accountant, sales). Common practice; no primary source read. [U]

---

## 3. Configurable data tables

- NN/g's four table tasks: find records, compare data, view/edit one row, act on records. Rules: first column is a human-readable identifier; freeze the header row and first column; make filters "discoverable, quick, and powerful" with a clear active indicator; allow hide/reorder columns without drag and show that columns are hidden; edit in a non-modal side panel rather than a modal; bulk actions through row checkboxes. [V] ([NN/g](https://www.nngroup.com/articles/data-tables/))
- Carbon: toolbar holds at most five actions; batch bar replaces row actions while rows are selected; only the sorted column shows a sort icon; pagination sits at the bottom. [V]
- **Shopify admin:** a view saves search and filters; column changes apply to the current view only; sort order is not saved; default views cannot be renamed; the leftmost column cannot be hidden or moved; managing views is desktop-only. [V] ([Shopify](https://help.shopify.com/en/manual/shopify-admin/productivity-tools/searching-filtering-views))
- **HubSpot:** views private by default; share as Private, Team, Everyone or Custom. [V]
- **Airtable:** personal versus collaborative views, favourites, locked views; permission to create depends on role. [V] ([Airtable](https://support.airtable.com/docs/getting-started-with-airtable-views))
- **Linear:** any filtered list can be saved as a view at workspace or team scope with an owner; favourites pin it to the sidebar; temporary filters travel in the URL. [V] ([Linear](https://linear.app/docs/custom-views))
- **Notion:** views store layout, filter, sort and visible properties, and filter changes can be kept personal or saved for everyone. [U] (help page returned 403)
- **ARIA grid versus table.** Use a native `<table>` for read-mostly lists; `role="grid"` is for editable or interactive cells and requires the author to manage arrow-key focus, with one tab stop for the whole grid. [V] ([APG grid](https://www.w3.org/WAI/ARIA/apg/patterns/grid/)). Our list screens are tables with links and a row menu, so native table semantics are correct and far cheaper.
- **Virtualisation threshold.** No source read gives a number (TanStack's guide URL has moved). Working rule: server-paginate at 25 to 50 rows and do not virtualise; only consider it above a few hundred rendered rows. [U]

---

## 4. Libraries

npm registry and download counts read on 2026-11-24 (week of 17 to 23 Nov). Sizes are Bundlephobia whole-package min+gzip, before tree-shaking, so treat as upper bounds. [V]

| Package | Latest stable | Published | Licence | Weekly downloads | gzip | React 19 peer |
|---|---|---|---|---|---|---|
| @dnd-kit/core | 6.3.1 | 2024-12 | MIT | 44.8M | 13.9 KB | yes (>=16.8) |
| @dnd-kit/sortable | 10.0.0 | 2024-12 | MIT | 43.1M | 3.6 KB | yes |
| @dnd-kit/react | 0.5.3 (pre-1.0) | 2026-10 | MIT | 3.4M | 32.4 KB | yes (^18 or ^19) |
| react-grid-layout | 2.3.0 | 2026-10 | MIT | 6.3M | 24.2 KB | yes (>=16.3; README says v2 needs 18+) |
| gridstack | 14.1.1 | 2026-11 | MIT | 1.0M | 26.4 KB | framework-agnostic |
| recharts | 3.11.1 | 2026-11 | MIT | 70.6M | 172.8 KB | yes |
| @visx/shape | 4.0.0 | 2026-06 | MIT | 11.6M | 10.5 KB (xychart 49 KB) | yes |
| @nivo/line | 0.99.0 | 2025-05 | MIT | 1.3M | 93.7 KB | yes |
| echarts | 6.1.0 | 2026-05 | Apache-2.0 | 11.4M | 359 KB | n/a (wrapper echarts-for-react 3.1.0, MIT) |
| chart.js | 4.6.2 | 2026-11 | MIT | 17.9M | 69 KB | via react-chartjs-2 5.3.1 |
| @tremor/react | 3.18.7 | 2025-01 | Apache-2.0 | 0.6M | 217 KB | **no (peer react ^18)** |
| uplot | 1.6.32 | 2025-03 | MIT | 1.1M | 21.3 KB | n/a |
| @observablehq/plot | 0.6.19 | 2026-11 | ISC | 1.3M | 124 KB | n/a |
| @tanstack/react-table | 9.3.1 | 2026-10 | MIT | 38.5M | 30.9 KB | yes (>=18) |
| @tanstack/react-virtual | 3.14.28 | 2026-11 | MIT | 47.1M | 10.5 KB | yes |
| ag-grid-community | 36.3.0 | 2026-11 | MIT | 5.6M | 383 KB | yes (ag-grid-react) |

Notes:

- **Grid.** `@dnd-kit/core` + `sortable` is already installed, is the most-used option, and is the only one here with documented keyboard and live-region support. It has had no release since December 2024, but is stable. `@dnd-kit/react` is the successor but is 0.x, which the stable-versions rule excludes. react-grid-layout 2.x is actively maintained and gives free placement and resize, but has no documented keyboard support, so we would have to build the accessible path ourselves anyway. gridstack is not React-native.
- **Charts.** Recharts is the default React choice, maintained (released the day before this research) and supports React 19, but it is the heaviest of the React options; load it lazily. Recharts 3 is reported to enable its keyboard `accessibilityLayer` by default. [U] (docs page 404). Tremor's npm package still pins React 18 and was last published January 2025, the month Vercel acquired it ([Vercel](https://vercel.com/blog/vercel-acquires-tremor) [S]); do not adopt. Nivo has not released since May 2025. ECharts and AG Grid are far too large for this use. uPlot and Chart.js draw to canvas, which makes text alternatives and theming harder. Visx is small and composable but you write the chart yourself.
- **Tables.** TanStack Table is headless, so it renders our own `<table>` and Tailwind styles and covers column visibility, order, sizing, pinning and sorting state that serialises straight into a saved view. v9 is the current stable line (docs list a "Migrating to V9" guide). [V] ([TanStack](https://tanstack.com/table/latest/docs/overview)). AG Grid Community includes virtualisation, sorting, filtering and pagination, but row grouping, pivot, server-side row model, Excel export and tool panels are Enterprise. [V] ([AG Grid](https://www.ag-grid.com/react-data-grid/community-vs-enterprise/))

**Smallest set that fits:** keep `@dnd-kit/core` + `@dnd-kit/sortable` (0 new KB); add `@tanstack/react-table`; add `recharts` only if and when a widget needs axes, tooltips or stacked series.

**When to hand-write SVG:** sparklines, KPI delta arrows, progress and bullet bars, a single-series bar strip, and a donut with one dominant share. These are one `<path>` or a few `<rect>`s, have no interaction, render in Server Components with zero client JS, and cannot cause layout shift. Use a library once a chart needs axes with tick logic, tooltips, multiple series, or responsive label collision.

---

## 5. Performance

- **Per-widget streaming.** Wrap each widget in its own `<Suspense>` with a skeleton fallback; Next.js recommends Suspense boundaries close to the data access rather than one page-level `loading.js`. Start independent fetches in parallel; sequential `await`s create waterfalls. [V] ([Next.js fetching](https://nextjs.org/docs/app/getting-started/fetching-data))
- **Server aggregation.** Widgets receive finished numbers and at most a few dozen points, never raw rows. One aggregate endpoint per widget type, so adding or removing a widget never changes another's query. [U]
- **Caching.** Next 16 `use cache` is stable (with `cacheComponents`). Default profile: 5 min client stale, 15 min server revalidate. It cannot read `cookies()`; read the session outside and pass `tenantId`/`userId` as arguments so they become part of the cache key. Invalidate with `cacheTag` + `updateTag` after a mutation (invoice created, payment recorded). The default in-memory cache does not persist across serverless instances. [V] ([use cache](https://nextjs.org/docs/app/api-reference/directives/use-cache))
- **Refresh.** Linear's dashboards are not live and expose "Refresh data". [V] For business figures, show "updated N min ago" with a manual refresh; do not poll. [U]
- **Layout shift.** Good CLS is 0.1 or less at the 75th percentile; reserve space for late content with fixed dimensions or `aspect-ratio`. [V] ([web.dev CLS](https://web.dev/articles/cls), [optimize CLS](https://web.dev/articles/optimize-cls)). Fixed widget size steps mean the skeleton and the loaded widget are the same box by construction.
- **Lazy code.** Load the chart library and the edit-mode drag code with `next/dynamic` so the default read-only dashboard ships neither. [U]

---

## Recommendations for C Note (ranked)

1. **One screen, action first.** Top-left is the number the owner acts on today (overdue receivables), then cash collected, then pipeline. Default to at most six KPI tiles above the fold; everything else is below or opt-in.
2. **Every KPI tile has the same anatomy:** label, compact value via `Intl.NumberFormat('en-IN', {notation:'compact'})`, delta versus previous period shown with arrow + sign + colour, hand-written SVG sparkline, exact value in a title/tooltip.
3. **Explicit edit mode with fixed size steps**, built on the already-installed `@dnd-kit/core` + `sortable` as an ordered list rendered into a CSS grid. No free-form resize, no react-grid-layout.
4. **Non-drag path is mandatory (WCAG 2.5.7):** per-widget menu with Move earlier/later, Size, Remove; keyboard sensor on; live-region announcements localised and 1-based.
5. **One persisted shape for both widget grids and table views:** versioned JSON per user per screen, validated and migrated on load, role-based defaults, "Reset to default".
6. **Tables: TanStack Table rendering a native `<table>`**, server-paginated, sticky header and first column, three densities (32/40/48 px), column show/hide/reorder through a menu as well as drag, saved views that are private by default and shareable to the team. Decide explicitly whether sort is part of a view (Shopify does not save it).
7. **Charts: bars and lines only**, at most four series, zero-based bars, direct labels, the four-colour AF palette, a text summary under each. Hand-write SVG for sparklines and simple bars; add Recharts lazily only when a widget needs axes and tooltips.
8. **Each widget is its own Suspense boundary and cached server aggregate**, keyed by tenant and range, tagged for invalidation on mutation, with a same-size skeleton, its own empty state (with the action that fills it) and its own error state with retry.
9. **Global time range with Indian presets** (this month, this quarter, financial year April to March) and "vs previous period" on by default.
10. **Mobile:** single column in saved order, edit mode uses the menu path only.

### Gaps to close before the design doc is final

- Notion, Material, Fluent and current Salesforce guidance could not be read directly (403/404/empty pages); claims are marked [U] or [S].
- Few's original essay and Tufte's data-ink principle are cited through secondary summaries.
- No source gave a KPI-count limit, a virtualisation threshold, or mobile dashboard rules; those are stated as working assumptions.
- Recharts 3 accessibility defaults and react-grid-layout keyboard support need a hands-on check if either library is adopted.
- Bundle sizes are whole-package figures; measure real impact with the Next bundle analyser.
