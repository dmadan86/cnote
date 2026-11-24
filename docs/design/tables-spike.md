# Tables phase 0 spike: Postgres performance of the JSONB records layout (25 Nov 2026)

Question: can one shared `records` relation (JSONB `data` keyed by field id, a link table, tenant RLS) serve grid views of 100k-record tables fast enough, and which indexes should the product create and when? Spike code: `apps/api/cmd/tablespike/` (throwaway, builds its own scratch database, never touches migrations). Raw output is in the appendix.

## Setup

- Machine: Apple M3 Pro, 11 cores, 18 GB RAM, macOS 26, local SSD. Postgres 17.11 (Homebrew), default config: shared_buffers 128MB, work_mem 4MB, maintenance_work_mem 64MB, effective_cache_size 4GB, max_parallel_workers_per_gather 2, default checkpoints, en_US.UTF-8. Go 1.24 + pgx v5, simple protocol (literals, so no generic-plan effects).
- Schema: `records(workspace_id, table_id, id, data jsonb, created_at, updated_at, deleted_at)`, PK `(workspace_id, table_id, id)`, fillfactor 80, plus the default-order partial index `(workspace_id, table_id, created_at, id) WHERE deleted_at IS NULL`. `links(workspace_id, from_record, field_id, to_record)` with PK `(from_record, field_id, to_record)` and a reverse index `(to_record, field_id, from_record)`. Tenant RLS from `core.enable_tenant_rls` (copied verbatim). Every measurement runs `SET LOCAL ROLE cnote_app` (non-superuser, RLS enforced).
- Data: tenant 1 has table A (100,000 records, 19 JSON fields plus a linked-record field: text, long text, 3 selects, multi-select array, number, currency minor units, date, datetime, checkbox, phone, email, URL, rating...) and table B (5,000 records, 150k link rows from A). Tenant 3 has two 5k tables. Tenant 2 is the noisy neighbour: one 500,000-record table in the same relation. Pass 1 ran before the neighbour was loaded (about 120k rows in the relation), pass 2 after (about 620k rows, heap 448 MB, about 720 B per record at fillfactor 80).
- Method: each query runs in its own tx, 1 warm-up plus 15 runs (5 if the first run took over 1.5 s), client-side time including draining every row; p95 of 15 runs is effectively the max. A separate `EXPLAIN (ANALYZE)` records the plan nodes. Writes: 100 single-record updates, 5 bulk transactions of 1,000, WAL bytes from `pg_current_wal_insert_lsn`, measured once right after a `CHECKPOINT` (includes full-page images) and once on the same rows again (warm).
- Strategies: S0 no data indexes. S1 one partial GIN on `data`. S2 per-field partial btrees (select, number, date, short text, company text). S3 = S1 + S2 + trigram GIN on the name field. S4 typed side table `sk(n05 float8, d07 date, s03, t01)` with btrees, joined back to `records` for the page. All indexes are partial: `WHERE table_id = <A> AND deleted_at IS NULL`.

## Finding 0 (most important): RLS silently disables the obvious indexes

As a non-superuser under RLS, Postgres only uses an index for a user qual if every function in it is `LEAKPROOF`. `jsonb ->>`, numeric comparison, `jsonb @>` and `ILIKE` are not. The naive plan from the research doc (GIN on `data`, btree on `(data->>'f')`) therefore never gets used by the app role; it works fine in psql as superuser, which is how this goes unnoticed.

Same raw expression indexes, same query, rare-match queries on the 620k-row relation:

| query | cnote_app (RLS) | superuser |
|---|---|---|
| `data->>'f12' = 'Company 77'` | 38.9 ms, no index | 0.3 ms, index |
| `(data->>'f05')::numeric BETWEEN 50000 AND 50050` | 49.2 ms, no index | 0.3 ms, index |
| `ORDER BY (data->>'f05')::numeric LIMIT 100` | 0.5 ms, index | 0.4 ms, index (ordering is not a qual, so sort indexes still work) |

What worked (validated end to end under RLS): wrapper functions declared `LEAKPROOF` (needs superuser, so migration role only):

- `jt(data, key) -> text`: `LANGUAGE internal` alias of `jsonb_object_field_text`, so it is not inlined. Text `=`, `<`, `>=` are leakproof, so ISO dates sort and range-filter as text.
- `jf(data, key) -> float8`: plpgsql, returns NULL unless `jsonb_typeof = 'number'`, so it can never raise. Numbers must be compared as `float8` (float8 operators are leakproof, numeric ones are not). Exact for integers below 2^53, so currency minor units are fine.
- Containment and ILIKE: own operators over leakproof internal functions, plus own GIN operator classes that copy the built-in support functions (`jsonb_path_ops` and `gin_trgm_ops`). `ALTER OPERATOR FAMILY ... ADD OPERATOR` is rejected because strategy numbers are taken. The operators need `RESTRICT = matchingsel` / `iclikesel`; without them the planner uses a default selectivity and ignores the GIN index (first run of the spike showed exactly that).
- Risks: creating LEAKPROOF functions needs superuser, which managed Postgres may not allow (check the target host before committing to this design); the fallback is to give the tables read path a role that owns the tables and enforce `workspace_id = $ws` in the query builder, losing RLS as a backstop for this module. Do not `ALTER FUNCTION pg_catalog.jsonb_contains LEAKPROOF`: it is not preserved by pg_dump or pg_upgrade. Collation: the date index should use `COLLATE "C"`; not tested.
- The query builder must therefore emit only these wrapper calls and operators; the spike's `queries.go` is a working reference.

## Results (tenant 1, table A, 100k records, noisy neighbour present, p50 / p95 ms)

Default view order is `created_at, id`; "rare" means fewer than 100 matches (no early exit). Plan names in parentheses.

| operation | S0 none | S1 GIN | S2 btree | S3 hybrid | S4 side table |
|---|---|---|---|---|---|
| select = X (12.5% of rows) | 0.7 / 0.9 (early exit) | 0.7 / 0.7 | 6.5 / 7.3 | 6.2 / 8.3 | 5.0 / 5.4 |
| text field = X, rare | 18 / 20 | 0.4 / 0.5 (gin) | 0.4 / 0.5 | 0.4 / 0.5 | 14 / 16 (field not in side table) |
| number range, rare (50 rows) | 70 / 74 | 70 / 73 | 0.3 / 0.4 | 0.3 / 0.4 | 0.5 / 0.9 |
| number range 1% | 7.3 / 7.5 | 7.4 / 8.0 | 0.9 / 1.3 | 1.0 / 1.2 | 1.2 / 1.3 |
| date range 2.8% | 2.1 / 2.2 | 2.2 / 2.2 | 1.9 / 2.0 | 1.9 / 2.1 | 1.8 / 1.9 |
| text contains (ILIKE '%x%'), rare | 41 / 43 | 42 / 43 | 61 / 69 (planner flip) | 2.3 / 2.4 (trigram) | 60 / 62 |
| text contains, 0.7% | 5.1 / 5.4 | 5.5 / 6.4 | 57 / 59 (planner flip) | 0.9 / 1.0 | 56 / 58 |
| combined (select + number + multi + checkbox) | 12 / 13 | 2.5 / 2.6 | 5.3 / 5.9 | 5.6 / 6.1 | 12 / 13 |
| filter + sort by number | 44 / 46 | 14.5 / 15 | 12 / 13 | 12 / 13 | 0.8 / 0.9 |
| sort number, OFFSET 0 | 82 / 87 | 83 / 87 | 0.6 / 0.8 | 0.6 / 0.7 | 0.6 / 0.9 |
| sort date, OFFSET 0 | 59 / 60 | 61 / 64 | 0.6 / 0.8 | 0.5 / 0.7 | 0.7 / 0.8 |
| sort text, OFFSET 0 | 68 / 99 | 66 / 73 | 0.6 / 0.7 | 0.6 / 0.7 | 0.7 / 0.8 |
| sort number, OFFSET 50,000 | 221 / 504 | 229 / 269 | 222 / 244 | 209 / 267 | 17 / 19 |
| sort date, OFFSET 50,000 | 362 / 456 | 369 / 438 | 358 / 415 | 348 / 384 | 17 / 18 |
| sort text, OFFSET 50,000 | 590 / 672 | 586 / 714 | 577 / 596 | 567 / 610 | 14 / 15 |
| keyset number, depth 50,000 | 101 / 112 | 99 / 101 | 0.6 / 0.8 | 0.6 / 0.8 | 0.7 / 0.8 |
| keyset date, depth 50,000 | 65 / 69 | 65 / 67 | 0.5 / 0.6 | 0.5 / 0.7 | 0.6 / 0.7 |
| keyset text, depth 50,000 | 77 / 79 | 76 / 78 | 0.5 / 0.5 | 0.5 / 0.5 | 0.6 / 0.6 |
| count with filter (select = X) | 36 / 37 | 6.7 / 7.3 | 5.1 / 6.4 | 5.0 / 5.6 | 3.7 / 4.3 |
| count with combined filter | 45 / 46 | 2.3 / 2.6 | 5.2 / 5.9 | 5.1 / 5.8 | 45 / 46 |
| count all | 4.2 / 4.6 (index-only) | 4.3 | 4.3 | 4.1 | 21 / 22 |
| group by select, counts | 42 / 45 | 41 / 43 | 42 / 43 | 41 / 42 | 28 / 28 |
| export all 100k (46 MB streamed) | 277 / 280 | 279 / 281 | 280 / 280 | 275 / 277 | 273 / 275 |

Linked records (same for every strategy): page of 100 with link count and linked labels 0.7 / 0.8 ms; records linked to a given target 0.4 / 0.8 ms; link counts for 100 target rows 1.2 / 1.3 ms. The `links` PK plus reverse index is enough.

Reading the table:

- Without a data index, anything that must examine many rows costs 40 to 90 ms at 100k (sorts, group by, filtered counts), 200 to 600 ms for a deep OFFSET, and scales linearly. Spot check on the 500k-record table: unindexed sort 410 ms, OFFSET 250,000 about 1.1 s with a 261 MB on-disk sort (work_mem 4 MB), unindexed rare filter 180 ms.
- A partial btree per field turns every filter and sort into 0.3 to 1 ms (first page) and keyset into 0.5 to 0.8 ms at any depth. The same spot check on 500k: range filter 1.3 ms, sorted page 0.6 ms, GIN containment 1.2 ms.
- OFFSET does not benefit from the btree: the planner picks the PK plus sort for OFFSET 50,000 (200 to 600 ms in S0 to S3). Only the narrow side table makes it cheap (14 to 17 ms), and that is still O(offset). Keyset needs no help.
- GIN is the only thing that speeds up array containment and multi-condition equality (combined 12 to 2.5 ms, count 45 to 2.3 ms). It does nothing for ranges, sorts or ILIKE. Trigram makes contains-search 1 to 2 ms instead of 5 to 60 ms.
- Common-value filters are the one case an index is slower: select = X (12.5%) is 0.7 ms with the default-order index and early exit but 5 to 6 ms through the select index plus top-N sort. Still fine.
- Planner instability is real: S2 chose the PK plus sort for ILIKE (57 ms vs 5 ms in S0); S0 `select = X` flipped between 0.7 ms and 38 ms between two spike runs; S3 `sort number OFFSET 50k` used the index with 43 ms in pass 1 and the PK plus sort with 209 ms in pass 2. Cause: no statistics for jsonb sub-fields until an expression index exists and has been ANALYZEd. Mitigation: ANALYZE right after creating an index (the spike does) and keep OFFSET shallow.
- Noisy neighbour (500k rows next door): partial-indexed queries were unaffected (S3 select = X 6.4 vs 6.2 ms, rare filter 0.4 vs 0.4, combined 5.4 vs 5.6). Unindexed fallback queries got worse (filtered count 14.6 to 36 ms) because they share heap and plan statistics with the other tenant. Index build time barely moved (GIN 1.2 to 1.4 s) with the heap fully cached; cold cache will cost more, not measured.

## Index sizes and build times (100k-record table, partial indexes)

| index | size | build | per record |
|---|---|---|---|
| btree on a text, number or date field | 3.9 to 5.6 MB | 0.1 to 0.35 s | 40 to 56 B |
| trigram GIN on a text field | 6.0 MB | 0.3 s | 60 B |
| GIN `jsonb_path_ops` on whole `data` | 34.5 MB | 1.2 to 1.5 s | 350 B |
| side table (typed, 4 columns + PK) plus 4 btrees | 21.6 MB + 17.5 MB | 0.75 s + 0.5 s | 390 B |
| baseline (PK, default order, links) | 126 MB over the 620k-row relation | n/a | about 200 B |

On the 500k-record table: btree 0.5 s, GIN 7.6 s. All figures are `CREATE INDEX` (blocking). `CREATE INDEX CONCURRENTLY` is typically 2 to 3 times slower and was not measured.

## Write amplification

Warm = rows already touched since the last checkpoint. After-checkpoint = first touch, includes full-page images (an 8 KB image per index page touched).

| strategy | update 1 record, unindexed field: WAL warm / after checkpoint, HOT | update 1 record, select + number: WAL warm, HOT | bulk update 1,000: p50 (p95) ms, WAL/row warm | bulk insert 1,000: p50 ms, WAL/row warm |
|---|---|---|---|---|
| S0 none | 416 B / 664 B, 100% | 688 B, 100% | 8.1 (8.4), 689 B | 5.6, 939 B |
| S1 GIN | 1,096 B / 6.7 KB, 0% | 1,360 B, 0% | 14.8 (15.1), 1,315 B | 7.1, 1,384 B |
| S2 btree (5 indexes) | 1,064 B / 42.5 KB, 0% | 1,360 B, 0% | 31.8 (32.3), 1,544 B | 26.9, 2,163 B |
| S3 hybrid (7 indexes) | 1,992 B / 43.4 KB, 0% | 2,304 B, 0% | 38.2 (83.6), 2,470 B | 61.3, 3,231 B |
| S4 side table | 416 B / 424 B, 100% | 1,296 B, 100% on records | 28.5 (30.1), 1,453 B | 21.2, 2,106 B |

- Single-cell edits cost 0.2 to 0.4 ms in every strategy, so interactive editing is not at risk. Amplification matters for bulk update, import and automations.
- With any index that references `data` (GIN or expression), no update is HOT, and every edit, even of an unindexed field, writes a new entry in every index: 2.5x WAL for S2, about 5x for S3 on a plain edit. Sparse indexes still pay that, because the index expressions depend on the whole `data` column. Side table S4 keeps `records` HOT (fillfactor 80) and only touches `sk` when a keyed field changes, at the price of a second write path to keep in sync.
- Each btree adds roughly 130 B WAL per row written; GIN about 400 to 700 B; first-touch full-page images dominate random single-row edits (7 to 44 KB) and make a nightly bulk import cheaper than many scattered edits, as usual.
- Importing 100k rows into a table with S3 indexes inserts at 61 ms per 1,000, so about 6 s for the inserts; building the indexes afterwards instead costs about 3 s. Import first, index after.

## Recommendation

**Storage layout.** Keep Option A: one shared `records` relation with JSONB `data` keyed by field id, a `links` table, `workspace_id, table_id` leading the PK, tenant RLS. Add: fillfactor 80 on `records`; the partial default-order index `(workspace_id, table_id, created_at, id) WHERE deleted_at IS NULL`; both link indexes. Add the leakproof helper functions, operators and the two operator classes in the first Tables migration (needs the migration role to be superuser; verify on the target host first). Do not start with the typed side table: it matches S2 on reads, wins only on write cost for edits to unindexed fields (about 0.6 KB WAL per edit) and deep OFFSET (which keyset removes), and costs a second write path. Keep it as the upgrade path for a table that outgrows the plan limits.

**Indexes to create automatically** (per table, partial on `table_id` and `deleted_at IS NULL`, built `CONCURRENTLY` and followed by `ANALYZE` by a worker, since `cnote_app` has no DDL rights; encapsulate that in a small index-manager component so the module stays extractable):

1. At table creation: nothing beyond the shared base indexes. Below about 5,000 live records create nothing (worst unindexed query is about 3 ms at that size).
2. When a field is used in a saved view's filter or sort (not an ad-hoc grid filter) and the table has more than 5,000 live records: btree `(wrapper(data, field), id)`, using `jf` for number, currency, percent, rating and `jt` for text, select, date, datetime, email, phone. Do not index checkbox alone.
3. When a view filters a multi-select field, or a view combines 2 or more equality filters, and the table has more than 20,000 records: one GIN per table (not per field).
4. When a view uses "contains" on a text field, and the table has more than 5,000 records: trigram GIN on that field (search terms shorter than 3 characters fall back to scan).
5. Drop an auto index when no view references the field for 30 days; cap at 8 btrees, 1 GIN, 2 trigram per table. Re-ANALYZE after any create.
6. Bulk import over 10,000 rows: insert first, then create indexes; or drop and rebuild.

**Pagination.** Keyset for the grid and the API: token = last sort value plus record id, always with `id` as tie-break (0.5 to 0.8 ms at depth 50,000 on any indexed sort; 65 to 100 ms unindexed). Page size 100 to 200. OFFSET only for the first few pages; reject OFFSET above 10,000 (200 to 600 ms at 50k on 100k records, over 1 s with disk sort at 250k on 500k). "Jump to row N" uses a count plus keyset from the nearest checkpoint, or is simply not offered. Filtered counts are 5 ms with an index, 36 to 45 ms without; cache them per view with a short TTL. Export: stream with a server cursor ordered by the default-order index (100k records, 46 MB in 0.28 s in-process; the real limit will be serialisation and the network).

**Practical limits** (proposal; 100k per table was tested end to end, 500k only spot-checked, anything above is extrapolation). All-in storage per record with all auto indexes is about 1.6 KB (0.72 KB heap, 0.2 KB baseline indexes, about 0.7 KB for the S3 set), so 100k records is about 160 MB and 1M about 1.6 GB.

| plan | records per table | records per workspace | notes |
|---|---|---|---|
| Free | 5,000 | 10,000 | no auto indexes needed, every query under 10 ms |
| Team | 50,000 | 150,000 | unindexed worst case about 35 ms (linear from 70 ms at 100k), indexed about 1 ms |
| Business | 100,000 | 500,000 | the tested configuration; index builds under 2 s per table |
| Scale (enterprise) | 500,000 | 2,000,000 | needs all auto indexes on sorted/filtered fields; GIN build 7.6 s; promote or partition by `table_id` beyond this |

Compare Airtable's 1k / 50k / 125k / 500k per base in the research report. Hard stops: at most 1,000 records per bulk update transaction (8 to 38 ms), 10,000 per import batch, 100 records per interactive page.

## Caveats (what this does not prove)

- One laptop, warm OS cache, no concurrent load, one connection, no network, autovacuum not exercised (manual VACUUM ANALYZE). Server defaults (shared_buffers 128MB); production will do better on the heap and worse on cold reads.
- Synthetic uniform data. Real columns have skew, NULLs (NULL ordering and `NULLS LAST` indexes not tested) and empty values.
- Simple protocol with literals. A prepared statement with `table_id` as a parameter can pick a generic plan that cannot use partial indexes; the query builder should inline the table id or set `plan_cache_mode = force_custom_plan`. Not tested.
- Managed Postgres may forbid `LEAKPROOF`; verify before building on it. Lookup/rollup/formula fields, record history and attachments were out of scope. Multi-select containment was only exercised inside `combined` and a low-selectivity `multi_contains` probe (0.7 ms in every strategy, default-order early exit); a very rare multi-select filter on GIN was not isolated.
- Throughput was measured sequentially; concurrent bulk writers contending on GIN pending lists and btree page splits were not.

## Reproduce

```
set -a; . ./.env; set +a
cd apps/api && go run ./cmd/tablespike -out /tmp/tables-spike-results.md   # about 4 minutes
psql "<admin url, db cnote_tables_spike>" -c 'select 1'                    # inspect, then drop the database
```
The program recreates `cnote_tables_spike` from `DATABASE_ADMIN_URL`, and leaves it in place so plans can be inspected; drop it afterwards.

## Appendix: raw results of the final run

Generated 2026-11-25 03:03 by apps/api/cmd/tablespike. Cells: `p50 / p95 ms [plan nodes]`; seq = sequential scan, par = parallel gather,
other names are the index used. Times are client-side round trips including draining all rows, inside one tx as role cnote_app (RLS enforced).

### Pass 1: no noisy neighbour (110k + 10k rows in the relation)

| query | S0 none | S3 hybrid |
|---|---|---|
| eq_select | 0.6 / 0.7 [base_created] | 6.4 / 7.5 [f03] |
| eq_rare | 14.4 / 15.8 [base_created] | 0.4 / 0.5 [f12] |
| range_rare | 67.1 / 67.3 [base_created] | 0.3 / 0.4 [f05] |
| text_ilike | 33.3 / 36.9 [seq] | 0.9 / 1.1 [trgm] |
| text_ilike_rare | 36.0 / 36.5 [seq] | 2.3 / 2.3 [trgm] |
| num_range | 7.1 / 7.2 [base_created] | 1.0 / 1.1 [f05] |
| combined | 24.6 / 25.4 [base_created] | 5.4 / 6.0 [f03] |
| sort_num_50k | 198.9 / 240.2 [seq] | 42.8 / 45.2 [f05] |
| count_eq | 14.6 / 15.7 [seq] | 4.9 / 5.3 [f03] |

### Pass 2: with the 500k-row neighbour (about 620k rows in the relation)

| query | S0 none | S1 gin | S2 btree | S3 hybrid | S4 side |
|---|---|---|---|---|---|
| eq_select | 0.7 / 0.9 [base_created] | 0.7 / 0.7 [base_created] | 6.5 / 7.3 [f03] | 6.2 / 8.3 [f03] | 5.0 / 5.4 [k03+records_pkey] |
| eq_rare | 18.3 / 20.1 [base_created] | 0.4 / 0.5 [gin] | 0.4 / 0.5 [f12] | 0.4 / 0.5 [f12] | 14.4 / 15.6 [base_created] |
| range_rare | 70.5 / 73.6 [base_created] | 69.5 / 72.6 [base_created] | 0.3 / 0.4 [f05] | 0.3 / 0.4 [f05] | 0.5 / 0.9 [k05+records_pkey] |
| text_ilike | 5.1 / 5.4 [base_created] | 5.5 / 6.4 [base_created] | 56.9 / 58.6 [records_pkey] | 0.9 / 1.0 [trgm] | 56.1 / 58.4 [records_pkey] |
| text_ilike_rare | 41.0 / 42.6 [base_created] | 42.1 / 43.4 [base_created] | 61.2 / 68.7 [records_pkey] | 2.3 / 2.4 [trgm] | 60.1 / 61.8 [records_pkey] |
| num_range | 7.3 / 7.5 [base_created] | 7.4 / 8.0 [base_created] | 0.9 / 1.3 [f05] | 1.0 / 1.2 [f05] | 1.2 / 1.3 [k05+records_pkey] |
| date_range | 2.1 / 2.2 [base_created] | 2.2 / 2.2 [base_created] | 1.9 / 2.0 [f07] | 1.9 / 2.1 [f07] | 1.8 / 1.9 [k07+records_pkey] |
| multi_contains | 0.7 / 0.8 [base_created] | 0.8 / 0.9 [base_created] | 0.7 / 0.9 [base_created] | 0.8 / 1.0 [base_created] | 0.7 / 0.9 [base_created] |
| combined | 12.1 / 13.2 [base_created] | 2.5 / 2.6 [gin] | 5.3 / 5.9 [f03] | 5.6 / 6.1 [f03] | 11.9 / 12.9 [base_created] |
| filter_sort_num | 44.2 / 46.2 [records_pkey] | 14.5 / 15.1 [gin] | 12.0 / 13.0 [f03] | 12.0 / 12.9 [f03] | 0.8 / 0.9 [k05+records_pkey] |
| sort_num_0 | 81.9 / 87.4 [records_pkey] | 83.4 / 87.3 [records_pkey] | 0.6 / 0.8 [f05] | 0.6 / 0.7 [f05] | 0.6 / 0.9 [k05+records_pkey] |
| sort_num_50k | 221.1 / 503.9 [records_pkey] | 229.0 / 268.6 [records_pkey] | 222.3 / 243.9 [records_pkey] | 209.0 / 267.4 [records_pkey] | 16.8 / 19.4 [k05+records_pkey] |
| sort_date_0 | 59.1 / 60.1 [records_pkey] | 60.5 / 64.3 [records_pkey] | 0.6 / 0.8 [f07] | 0.5 / 0.7 [f07] | 0.7 / 0.8 [k07+records_pkey] |
| sort_date_50k | 362.2 / 455.9 [records_pkey] | 368.8 / 438.3 [records_pkey] | 358.2 / 414.5 [records_pkey] | 348.3 / 383.9 [records_pkey] | 17.0 / 18.0 [k07+records_pkey] |
| sort_num_50k_2step | 101.9 / 106.1 [records_pkey] | 102.0 / 136.2 [records_pkey] | 104.1 / 144.8 [records_pkey] | 115.3 / 160.7 [records_pkey] | 99.5 / 102.3 [records_pkey] |
| sort_text_50k_2step | 431.6 / 462.4 [records_pkey] | 436.5 / 495.5 [records_pkey] | 430.6 / 443.9 [records_pkey] | 429.9 / 466.0 [records_pkey] | 428.2 / 436.2 [records_pkey] |
| sort_text_0 | 67.6 / 98.7 [records_pkey] | 65.6 / 72.7 [records_pkey] | 0.6 / 0.7 [f01] | 0.6 / 0.7 [f01] | 0.7 / 0.8 [k01+records_pkey] |
| sort_text_50k | 590.3 / 672.1 [records_pkey] | 586.4 / 714.1 [records_pkey] | 576.8 / 596.2 [records_pkey] | 566.5 / 610.2 [records_pkey] | 14.3 / 15.1 [k01+records_pkey] |
| keyset_num_50k | 101.2 / 111.7 [records_pkey] | 99.2 / 101.0 [records_pkey] | 0.6 / 0.8 [f05] | 0.6 / 0.8 [f05] | 0.7 / 0.8 [k05+records_pkey] |
| keyset_date_50k | 65.3 / 68.5 [records_pkey] | 64.5 / 67.0 [records_pkey] | 0.5 / 0.6 [f07] | 0.5 / 0.7 [f07] | 0.6 / 0.7 [k07+records_pkey] |
| keyset_text_50k | 77.2 / 78.8 [records_pkey] | 76.3 / 77.8 [records_pkey] | 0.5 / 0.5 [f01] | 0.5 / 0.5 [f01] | 0.6 / 0.6 [k01+records_pkey] |
| count_eq | 36.0 / 36.9 [records_pkey] | 6.7 / 7.3 [gin] | 5.1 / 6.4 [f03] | 5.0 / 5.6 [f03] | 3.7 / 4.3 [k03] |
| count_all | 4.2 / 4.6 [base_created(only)] | 4.3 / 4.8 [base_created(only)] | 4.3 / 4.5 [base_created(only)] | 4.1 / 4.4 [base_created(only)] | 20.7 / 21.7 [seq] |
| count_combined | 44.8 / 46.3 [records_pkey] | 2.3 / 2.6 [gin] | 5.2 / 5.9 [f03] | 5.1 / 5.8 [f03] | 45.1 / 45.8 [records_pkey] |
| group_by_select | 41.6 / 44.5 [records_pkey] | 41.3 / 42.6 [records_pkey] | 42.0 / 42.9 [records_pkey] | 41.1 / 41.7 [records_pkey] | 27.5 / 27.8 [seq] |
| link_page | 0.7 / 0.8 [links] | 0.7 / 0.8 [links] | 0.7 / 1.0 [links] | 0.7 / 0.8 [links] | 0.6 / 0.7 [links] |
| link_filter | 0.4 / 0.8 [links_rev+records_pkey] | 0.4 / 0.5 [links_rev+records_pkey] | 0.4 / 0.5 [links_rev+records_pkey] | 0.4 / 0.5 [links_rev+records_pkey] | 0.3 / 0.4 [links_rev+records_pkey] |
| link_backcount | 1.2 / 1.3 [links_rev] | 1.2 / 1.4 [links_rev] | 1.2 / 1.3 [links_rev] | 1.3 / 1.6 [links_rev] | 1.3 / 1.3 [links_rev] |
| export_all | 276.7 / 280.3 [46 MB streamed] | 278.5 / 280.9 [46 MB streamed] | 279.7 / 279.8 [46 MB streamed] | 274.7 / 276.5 [46 MB streamed] | 273.2 / 274.5 [46 MB streamed] |

### Index and side-table builds

| pass | strategy | object | build ms | size MB |
|---|---|---|---|---|
| pass1 | S3 hybrid | ix_gin | 1201 | 34.5 |
| pass1 | S3 hybrid | ix_f03 | 122 | 4.1 |
| pass1 | S3 hybrid | ix_f05 | 67 | 3.9 |
| pass1 | S3 hybrid | ix_f07 | 167 | 4.8 |
| pass1 | S3 hybrid | ix_f01 | 323 | 5.6 |
| pass1 | S3 hybrid | ix_f12 | 198 | 4.8 |
| pass1 | S3 hybrid | ix_trgm | 272 | 6.0 |
| pass2 | S1 gin | ix_gin | 1456 | 34.5 |
| pass2 | S2 btree | ix_f03 | 142 | 4.1 |
| pass2 | S2 btree | ix_f05 | 109 | 3.9 |
| pass2 | S2 btree | ix_f07 | 202 | 4.8 |
| pass2 | S2 btree | ix_f01 | 349 | 5.6 |
| pass2 | S2 btree | ix_f12 | 220 | 4.8 |
| pass2 | S3 hybrid | ix_gin | 1362 | 34.5 |
| pass2 | S3 hybrid | ix_f03 | 116 | 4.1 |
| pass2 | S3 hybrid | ix_f05 | 96 | 3.9 |
| pass2 | S3 hybrid | ix_f07 | 202 | 4.8 |
| pass2 | S3 hybrid | ix_f01 | 365 | 5.6 |
| pass2 | S3 hybrid | ix_f12 | 232 | 4.8 |
| pass2 | S3 hybrid | ix_trgm | 312 | 6.0 |
| pass2 | S4 side | sk table + pk (populate) | 745 | 21.6 |
| pass2 | S4 side | ix_k03 | 101 | 4.1 |
| pass2 | S4 side | ix_k05 | 42 | 3.9 |
| pass2 | S4 side | ix_k07 | 37 | 3.9 |
| pass2 | S4 side | ix_k01 | 351 | 5.6 |

### Writes (pass 2)

| strategy | op | p50 ms (warm) | p95 ms (warm) | WAL bytes/row warm | WAL bytes/row after checkpoint | HOT update % |
|---|---|---|---|---|---|---|
| S0 none | update 1 record, unindexed field (f02) | 0.3 | 1.0 | 416 | 664 | 100 |
| S0 none | update 1 record, select+number (f03,f05) | 0.2 | 0.3 | 688 | 696 | 100 |
| S0 none | bulk update 1000, select+number | 8.1 | 8.4 | 689 | 3892 | 75 |
| S0 none | bulk insert 1000 (rolled back) | 5.6 | 6.1 | 939 | 1697 | n/a |
| S1 gin | update 1 record, unindexed field (f02) | 0.2 | 0.3 | 1096 | 6680 | 0 |
| S1 gin | update 1 record, select+number (f03,f05) | 0.3 | 0.5 | 1360 | 7768 | 0 |
| S1 gin | bulk update 1000, select+number | 14.8 | 15.1 | 1315 | 4499 | 0 |
| S1 gin | bulk insert 1000 (rolled back) | 7.1 | 7.1 | 1384 | 2172 | n/a |
| S2 btree | update 1 record, unindexed field (f02) | 0.3 | 0.5 | 1064 | 42528 | 0 |
| S2 btree | update 1 record, select+number (f03,f05) | 0.3 | 0.5 | 1360 | 43424 | 0 |
| S2 btree | bulk update 1000, select+number | 31.8 | 32.3 | 1544 | 5297 | 0 |
| S2 btree | bulk insert 1000 (rolled back) | 26.9 | 32.4 | 2163 | 3047 | n/a |
| S3 hybrid | update 1 record, unindexed field (f02) | 0.3 | 0.5 | 1992 | 43440 | 0 |
| S3 hybrid | update 1 record, select+number (f03,f05) | 0.3 | 0.6 | 2304 | 44336 | 0 |
| S3 hybrid | bulk update 1000, select+number | 38.2 | 83.6 | 2470 | 6255 | 0 |
| S3 hybrid | bulk insert 1000 (rolled back) | 61.3 | 134.5 | 3231 | 4113 | n/a |
| S4 side | update 1 record, unindexed field (f02) | 0.3 | 0.3 | 416 | 424 | 100 |
| S4 side | update 1 record, select+number (f03,f05) | 0.4 | 0.6 | 1296 | 36408 | 100 |
| S4 side | bulk update 1000, select+number | 28.5 | 30.1 | 1453 | 5274 | 100 |
| S4 side | bulk insert 1000 (rolled back) | 21.2 | 27.3 | 2106 | 3767 | n/a |

### Sizes (pass 2, after load)

- S0 none: records heap 448 MB, table incl. TOAST 448 MB, all indexes on records 126 MB
- S1 gin: records heap 450 MB, table incl. TOAST 450 MB, all indexes on records 163 MB
- S2 btree: records heap 450 MB, table incl. TOAST 450 MB, all indexes on records 153 MB
- S3 hybrid: records heap 450 MB, table incl. TOAST 450 MB, all indexes on records 193 MB
- S4 side: records heap 450 MB, table incl. TOAST 450 MB, all indexes on records 129 MB

### RLS and index usability (raw expressions vs leakproof helpers)

Raw expression indexes (`(data->>'f03')`, `((data->>'f05')::numeric)`) created, same query as cnote_app (RLS) and as superuser (bypasses RLS).

| query | cnote_app p50 / p95 ms [plan] | superuser p50 / p95 ms [plan] |
|---|---|---|
| eq_rare (data->>'f12' = ...) | 38.9 / 39.8 [records_pkey] | 0.3 / 0.4 [raw12] |
| range_rare ((data->>'f05')::numeric) | 49.2 / 54.9 [records_pkey] | 0.3 / 0.4 [raw05] |
| sort_num_0 | 0.5 / 0.5 [raw05] | 0.4 / 0.4 [raw05] |
