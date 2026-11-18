'use client'
import { ChevronLeft, Download, Trash2, X } from 'lucide-react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Confirm } from '@/components/billing/bits'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { FilterBar, filterReady, filterText } from './filters'
import {
  bucketEnd,
  type Dashboard,
  type DatasetInfo,
  defaultSpec,
  download,
  errText,
  type Filter,
  fmtDate,
  type Measure,
  measureKey,
  measureLabel,
  PRESETS,
  type Report,
  type RunResult,
  runCmd,
  type Spec,
  summarySentence,
  todayIST,
} from './lib'
import { Bars, BigNumber, DataTable } from './result'
import { TEMPLATES } from './templates'

const BUCKETS = ['day', 'week', 'month', 'quarter', 'fy']
const BUCKET_LABEL: Record<string, string> = {
  day: 'Day',
  week: 'Week',
  month: 'Month',
  quarter: 'Quarter',
  fy: 'Financial year',
}

/** What the server runs: half-typed filters stay in the panel only. */
const clean = (s: Spec): Spec => ({
  ...s,
  filters: (s.filters ?? []).filter(filterReady),
  period: s.period?.preset === 'custom' && !(s.period.from && s.period.to) ? null : s.period,
})
const aggregate = (s: Spec) => !!s.group_by?.length || !!s.measures?.length

function RangeInputs({ from, to, set }: { from: string; to: string; set: (f: string, t: string) => void }) {
  return (
    <div className="flex items-center gap-2 text-[13px]">
      <input
        type="date"
        aria-label="From"
        value={from}
        onChange={(e) => set(e.target.value, to)}
        className={inputCls}
      />
      to
      <input
        type="date"
        aria-label="To"
        value={to}
        onChange={(e) => set(from, e.target.value)}
        className={inputCls}
      />
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section aria-label={title} className="space-y-2">
      <h2 className="text-[12px] font-semibold uppercase tracking-wide text-muted">{title}</h2>
      {children}
    </section>
  )
}

function Panel({
  spec,
  set,
  ds,
  datasets,
  pickDataset,
  periodLabel,
}: {
  spec: Spec
  set: (s: Spec) => void
  ds: DatasetInfo
  datasets: DatasetInfo[]
  pickDataset: (key: string) => void
  periodLabel?: string
}) {
  const dateFields = ds.fields.filter((f) => f.type === 'date')
  const group = spec.group_by?.[0]
  const gField = ds.fields.find((f) => f.key === group?.field)
  const measures = spec.measures ?? []
  const measureOpts: Measure[] = [
    { agg: 'count' },
    ...ds.fields.flatMap((f) => f.aggs.filter((a) => a !== 'count').map((agg) => ({ field: f.key, agg }))),
  ]
  const p = spec.period
  const setGroup = (field: string) => {
    const f = ds.fields.find((x) => x.key === field)
    if (!f) {
      set({
        ...spec,
        group_by: [],
        sort: [],
        measures:
          measures.length === 1 && measures[0].agg === 'count' && spec.viz !== 'number' ? [] : measures,
        viz: spec.viz === 'bar' ? 'table' : spec.viz,
      })
      return
    }
    const date = f.type === 'date'
    set({
      ...spec,
      group_by: [{ field, ...(date ? { bucket: 'month' } : {}) }],
      measures: measures.length ? measures : [{ agg: 'count' }],
      sort: [{ key: date ? 'g0' : 'm0', dir: date ? 'asc' : 'desc' }],
      viz: spec.viz === 'number' ? 'table' : spec.viz,
    })
  }
  const vizOpts: [Spec['viz'], string, string | null][] = [
    ['table', 'Table', null],
    ['bar', 'Bar', group ? null : 'Bar needs a group: choose one under Summarise'],
    [
      'number',
      'Number',
      group ? 'Number needs no group' : measures.length > 1 ? 'Number shows one measure' : null,
    ],
  ]
  const cols = spec.columns ?? []
  return (
    <div className="space-y-5">
      <Section title="What do you want to see?">
        <Field label="Report on">
          {(id) => (
            <select
              id={id}
              className={inputCls}
              value={spec.dataset}
              onChange={(e) => pickDataset(e.target.value)}
            >
              {datasets.map((d) => (
                <option key={d.key} value={d.key}>
                  {d.label}
                </option>
              ))}
            </select>
          )}
        </Field>
        <p className="text-[12px] text-muted">{ds.description}</p>
        <Field label="Period">
          {(id) => (
            <select
              id={id}
              className={inputCls}
              value={p?.preset ?? 'none'}
              onChange={(e) => {
                const v = e.target.value
                set({
                  ...spec,
                  period:
                    v === 'none'
                      ? null
                      : {
                          field: p?.field ?? ds.default_date,
                          preset: v,
                          ...(v === 'custom' ? { from: p?.from ?? '', to: p?.to ?? '' } : {}),
                        },
                })
              }}
            >
              <option value="none">All time</option>
              {PRESETS.map(([k, l]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
          )}
        </Field>
        {p && p.preset === 'custom' && (
          <RangeInputs
            from={p.from ?? ''}
            to={p.to ?? ''}
            set={(from, to) => set({ ...spec, period: { ...p, from, to } })}
          />
        )}
        {p && dateFields.length > 1 && (
          <Field label="Period applies to">
            {(id) => (
              <select
                id={id}
                className={inputCls}
                value={p.field}
                onChange={(e) => set({ ...spec, period: { ...p, field: e.target.value } })}
              >
                {dateFields.map((f) => (
                  <option key={f.key} value={f.key}>
                    {f.label}
                  </option>
                ))}
              </select>
            )}
          </Field>
        )}
        {p && periodLabel && <p className="text-[12px] text-muted">{periodLabel}</p>}
        <div className="space-y-1">
          <span className="block text-[12px] font-medium text-muted">Only include</span>
          <FilterBar
            fields={ds.fields}
            filters={spec.filters ?? []}
            onChange={(filters) => set({ ...spec, filters })}
          />
        </div>
      </Section>

      <details open={aggregate(spec)} className="group rounded-md border border-line">
        <summary className="cursor-pointer select-none px-3 py-2 text-[13px] font-medium">Summarise</summary>
        <div className="space-y-3 border-t border-line p-3">
          <Field label="Group by">
            {(id) => (
              <select
                id={id}
                className={inputCls}
                value={group?.field ?? ''}
                onChange={(e) => setGroup(e.target.value)}
              >
                <option value="">Nothing (list rows)</option>
                {ds.fields
                  .filter((f) => f.group)
                  .map((f) => (
                    <option key={f.key} value={f.key}>
                      {f.label}
                    </option>
                  ))}
              </select>
            )}
          </Field>
          {gField?.type === 'date' && group && (
            <Field label="By">
              {(id) => (
                <select
                  id={id}
                  className={inputCls}
                  value={group.bucket ?? 'month'}
                  onChange={(e) =>
                    set({ ...spec, group_by: [{ field: group.field, bucket: e.target.value }] })
                  }
                >
                  {BUCKETS.map((b) => (
                    <option key={b} value={b}>
                      {BUCKET_LABEL[b]}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          )}
          <div className="space-y-1.5">
            <span className="block text-[12px] font-medium text-muted">Show</span>
            {measures.map((m, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: the same measure may be picked twice
              <div key={i} className="flex items-center gap-1.5">
                <select
                  aria-label={`Measure ${i + 1}`}
                  className={inputCls}
                  value={measureKey(m)}
                  onChange={(e) =>
                    set({
                      ...spec,
                      measures: measures.map((x, j) =>
                        j === i ? (measureOpts.find((o) => measureKey(o) === e.target.value) ?? x) : x,
                      ),
                    })
                  }
                >
                  {measureOpts.map((o) => (
                    <option key={measureKey(o)} value={measureKey(o)}>
                      {measureLabel(o, ds)}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  aria-label={`Remove ${measureLabel(m, ds)}`}
                  className="rounded p-1.5 hover:bg-hover"
                  onClick={() => set({ ...spec, measures: measures.filter((_, j) => j !== i) })}
                >
                  <X size={14} aria-hidden />
                </button>
              </div>
            ))}
            {measures.length < 6 && (
              <select
                aria-label="Add a measure"
                className={inputCls}
                value=""
                onChange={(e) => {
                  const m = measureOpts.find((o) => measureKey(o) === e.target.value)
                  if (m) set({ ...spec, measures: [...measures, m] })
                }}
              >
                <option value="">+ Add up something</option>
                {measureOpts
                  .filter((o) => !measures.some((m) => measureKey(m) === measureKey(o)))
                  .map((o) => (
                    <option key={measureKey(o)} value={measureKey(o)}>
                      {measureLabel(o, ds)}
                    </option>
                  ))}
              </select>
            )}
          </div>
        </div>
      </details>

      <Section title="Show as">
        <fieldset className="flex gap-1">
          <legend className="sr-only">Show as</legend>
          {vizOpts.map(([v, label, why]) => (
            <button
              key={v}
              type="button"
              aria-pressed={(spec.viz ?? 'table') === v}
              disabled={!!why}
              title={why ?? undefined}
              onClick={() =>
                set({
                  ...spec,
                  viz: v,
                  ...(v === 'number' && !measures.length ? { measures: [{ agg: 'count' }] } : {}),
                })
              }
              className="rounded-md border border-line px-3 py-1.5 text-[13px] hover:bg-hover disabled:opacity-40 aria-pressed:border-accent aria-pressed:bg-hover aria-pressed:font-medium"
            >
              {label}
            </button>
          ))}
        </fieldset>
        {vizOpts.map(
          ([v, , why]) =>
            why && (
              <p key={v} className="text-[12px] text-muted">
                {why}.
              </p>
            ),
        )}
      </Section>

      {!aggregate(spec) && (
        <Section title="Columns">
          <div className="flex flex-wrap gap-1.5">
            {cols.map((k) => (
              <span
                key={k}
                className="inline-flex items-center rounded-full border border-line bg-side py-0.5 pl-2.5 pr-1 text-[12px]"
              >
                {ds.fields.find((f) => f.key === k)?.label ?? k}
                <button
                  type="button"
                  disabled={cols.length < 2}
                  aria-label={`Remove column ${ds.fields.find((f) => f.key === k)?.label ?? k}`}
                  className="ml-0.5 rounded-full p-0.5 hover:bg-hover disabled:opacity-30"
                  onClick={() => set({ ...spec, columns: cols.filter((c) => c !== k) })}
                >
                  <X size={12} aria-hidden />
                </button>
              </span>
            ))}
          </div>
          <select
            aria-label="Add a column"
            className={inputCls}
            value=""
            onChange={(e) => e.target.value && set({ ...spec, columns: [...cols, e.target.value] })}
          >
            <option value="">+ Add a column</option>
            {ds.fields
              .filter((f) => !cols.includes(f.key))
              .map((f) => (
                <option key={f.key} value={f.key}>
                  {f.label}
                </option>
              ))}
          </select>
        </Section>
      )}
    </div>
  )
}

/** Same spec + one more filter on the clicked group value, as plain rows. */
function drillSpec(spec: Spec, ds: DatasetInfo, res: RunResult, row: number): Spec | null {
  const g = spec.group_by?.[0]
  const f = ds.fields.find((x) => x.key === g?.field)
  if (!g || !f) return null
  const v = res.rows[row]?.[0]
  let filter: Filter
  if (v === null || v === undefined || v === '') {
    if (f.type === 'enum' || f.type === 'money' || f.type === 'number') return null
    filter = { field: f.key, op: 'empty' }
  } else if (f.type === 'person' || f.type === 'contact')
    filter = { field: f.key, op: 'in', value: [(v as { id: string }).id] }
  else if (f.type === 'enum') filter = { field: f.key, op: 'in', value: [v] }
  else if (f.type === 'date')
    filter = { field: f.key, op: 'between', value: [v, bucketEnd(String(v), g.bucket)] }
  else if (f.type === 'bool') filter = { field: f.key, op: 'is', value: v }
  else filter = { field: f.key, op: 'eq', value: v }
  return {
    dataset: spec.dataset,
    columns: ds.default_columns,
    filters: [...(spec.filters ?? []), filter],
    period: spec.period,
    viz: 'table',
  }
}

function Drill({
  ws,
  spec,
  ds,
  label,
  onClose,
}: {
  ws: string
  spec: Spec
  ds: DatasetInfo
  label: string
  onClose: () => void
}) {
  const [res, setRes] = useState<RunResult>()
  const [err, setErr] = useState<string>()
  useEffect(() => {
    const ac = new AbortController()
    runCmd<RunResult>(ws, 'report.run', { spec }, ac.signal)
      .then(setRes)
      .catch((e) => !ac.signal.aborted && setErr(errText(e)))
    return () => ac.abort()
  }, [ws, spec])
  return (
    <Modal open title={`Rows for ${label}`} onClose={onClose} className="max-w-4xl">
      <ErrorText error={err} />
      {!res && !err && <p className="text-[13px] text-muted">Loading...</p>}
      {res && (
        <>
          {res.rows.length === 0 ? (
            <p className="text-[13px] text-muted">No rows.</p>
          ) : (
            <DataTable res={res} spec={spec} ds={ds} />
          )}
          {res.truncated && (
            <p className="mt-2 text-[12px] text-muted">
              Showing first {res.rows.length.toLocaleString('en-IN')} of {res.count.toLocaleString('en-IN')}{' '}
              rows.
            </p>
          )}
        </>
      )}
    </Modal>
  )
}

function SaveModal({
  ws,
  spec,
  report,
  defaultName,
  onClose,
  onSaved,
}: {
  ws: string
  spec: Spec
  report?: Report
  defaultName: string
  onClose: () => void
  onSaved: (r: Report) => void
}) {
  const [name, setName] = useState(report?.name ?? defaultName)
  const [vis, setVis] = useState<string>(report?.visibility ?? 'private')
  const [dash, setDash] = useState('')
  const [dashName, setDashName] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const dashboards = useCmd<{ dashboards: Dashboard[] }>(ws, 'dashboard.list')
  const asCopy = !!report && !report.can_edit
  return (
    <Modal open title="Save report" onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          setBusy(true)
          setErr(undefined)
          try {
            const { report: r } = await cmd<{ report: Report }>(ws, 'report.save', {
              ...(report && !asCopy && { id: report.id }),
              name: name.trim(),
              spec: clean(spec),
              visibility: vis,
            })
            if (dash) {
              const size = spec.viz === 'number' ? 'number' : 'half'
              const d = dashboards.data?.dashboards.find((x) => x.id === dash)
              await cmd(ws, 'dashboard.save', {
                ...(d && { id: d.id }),
                name: d ? d.name : dashName.trim(),
                tiles: [...(d?.tiles ?? []), { report_id: r.id, size }],
                period: d?.period ?? null,
                visibility: d?.visibility ?? 'private',
              })
            }
            onSaved(r)
          } catch (x) {
            setErr(errText(x))
            setBusy(false)
          }
        }}
      >
        <Field label="Name">
          {(id) => (
            <input
              id={id}
              required
              className={inputCls}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          )}
        </Field>
        <Field label="Who can see it">
          {(id) => (
            <select id={id} className={inputCls} value={vis} onChange={(e) => setVis(e.target.value)}>
              <option value="private">Only me</option>
              <option value="workspace">Everyone in the workspace</option>
            </select>
          )}
        </Field>
        <Field label="Add to dashboard">
          {(id) => (
            <select id={id} className={inputCls} value={dash} onChange={(e) => setDash(e.target.value)}>
              <option value="">Not now</option>
              {dashboards.data?.dashboards
                .filter((d) => d.can_edit)
                .map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name}
                  </option>
                ))}
              <option value="new">New dashboard...</option>
            </select>
          )}
        </Field>
        {dash === 'new' && (
          <Field label="Dashboard name">
            {(id) => (
              <input
                id={id}
                required
                className={inputCls}
                value={dashName}
                onChange={(e) => setDashName(e.target.value)}
              />
            )}
          </Field>
        )}
        {asCopy && (
          <p className="text-[12px] text-muted">
            This report belongs to {report?.owner_name}; saving makes your own copy.
          </p>
        )}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={busy || !name.trim()}>
            Save report
          </button>
        </div>
      </form>
    </Modal>
  )
}

export function Builder({ id, template }: { id?: string; template?: string }) {
  const { ws } = useShell()
  const router = useRouter()
  const datasets = useCmd<{ datasets: DatasetInfo[] }>(ws, 'report.datasets')
  const saved = useCmd<{ report: Report }>(ws, 'report.get', { id }, !!id)
  const [spec, setSpec] = useState<Spec>()
  const [name, setName] = useState('')
  const [report, setReport] = useState<Report>()
  const [res, setRes] = useState<RunResult>()
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState<string>()
  const [drill, setDrill] = useState<{ spec: Spec; label: string }>()
  const [save, setSave] = useState(false)
  const [del, setDel] = useState(false)
  const [note, setNote] = useState<string>()
  const started = useRef(false)

  const dsList = datasets.data?.datasets
  // Initial spec: saved report, template, or the first dataset. Runs once when its inputs arrive.
  useEffect(() => {
    if (started.current || !dsList?.length) return
    if (id) {
      if (!saved.data) return
      setReport(saved.data.report)
      setName(saved.data.report.name)
      setSpec(saved.data.report.spec)
    } else {
      const t = TEMPLATES.find((x) => x.id === template)
      const first = dsList.find((d) => d.key === t?.spec.dataset) ?? dsList[0]
      const s: Spec =
        t && first.key === t.spec.dataset
          ? JSON.parse(JSON.stringify(t.spec).replaceAll('__today__', todayIST()))
          : defaultSpec(first)
      setSpec(s)
      setName(t?.title.replace(/\?$/, '') ?? '')
    }
    started.current = true
  }, [dsList, id, saved.data, template])

  const ds = dsList?.find((d) => d.key === spec?.dataset)
  const runSpec = useMemo(() => (spec ? clean(spec) : undefined), [spec])
  const key = JSON.stringify(runSpec)
  // biome-ignore lint/correctness/useExhaustiveDependencies: key stands in for runSpec
  useEffect(() => {
    if (!runSpec || !ds) return
    const ac = new AbortController()
    const t = setTimeout(() => {
      setLoading(true)
      runCmd<RunResult>(ws, 'report.run', { spec: runSpec }, ac.signal)
        .then((r) => {
          setRes(r)
          setErr(undefined)
        })
        .catch((e) => {
          if (ac.signal.aborted) return
          setRes(undefined) // never leave old totals under the new filters
          setErr(errText(e))
        })
        .finally(() => !ac.signal.aborted && setLoading(false))
    }, 400)
    return () => {
      clearTimeout(t)
      ac.abort()
    }
  }, [ws, key, !!ds])

  if (datasets.error || saved.error) return <ErrorText error={datasets.error ?? saved.error} />
  if (dsList && !dsList.length)
    return <p className="text-muted">No reportable data is available to you yet.</p>
  if (!spec || !ds || !dsList)
    return <div aria-busy="true" className="h-40 animate-pulse rounded-lg bg-hover" />

  const pickDataset = (k: string) => {
    const d = dsList.find((x) => x.key === k)
    if (d) setSpec(defaultSpec(d))
  }
  const sentence = summarySentence(spec, ds)
  const grouped = !!spec.group_by?.length
  const doDrill = (row: number) => {
    if (!res) return
    const s = drillSpec(clean(spec), ds, res, row)
    if (s)
      setDrill({
        spec: s,
        label: String(
          res.rows[row][0] && typeof res.rows[row][0] === 'object'
            ? (res.rows[row][0] as { label: string }).label
            : (res.rows[row][0] ?? '(none)'),
        ),
      })
  }
  const exportCsv = async () => {
    setNote(undefined)
    try {
      const r = await cmd<{ csv: string; filename: string }>(ws, 'report.run', {
        spec: clean(spec),
        format: 'csv',
      })
      download(r.filename || 'report.csv', 'text/csv', r.csv)
    } catch (e) {
      setNote(errText(e))
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Link
          href={`/w/${ws}/reports`}
          className="inline-flex items-center gap-0.5 text-[13px] text-muted hover:text-fg"
        >
          <ChevronLeft size={14} aria-hidden /> Reports
        </Link>
        <input
          aria-label="Report name"
          placeholder={sentence}
          value={name}
          readOnly={!!report && !report.can_edit}
          onChange={(e) => setName(e.target.value)}
          className="min-w-48 flex-1 rounded-md border border-transparent bg-transparent px-2 py-1 text-xl font-semibold hover:border-line focus:border-line"
        />
        {report && <Pill>{report.visibility === 'workspace' ? 'Workspace' : 'Private'}</Pill>}
        <button type="button" className={btn} onClick={exportCsv}>
          <Download size={14} aria-hidden /> Export CSV
        </button>
        {report?.can_edit && (
          <button type="button" className={btn} onClick={() => setDel(true)}>
            <Trash2 size={14} aria-hidden /> Delete
          </button>
        )}
        <button type="button" className={btnPrimary} onClick={() => setSave(true)}>
          {report && !report.can_edit ? 'Save a copy' : 'Save'}
        </button>
      </div>
      <ErrorText error={note} />
      <div className="grid gap-6 lg:grid-cols-[22rem_1fr]">
        <Panel
          spec={spec}
          set={setSpec}
          ds={ds}
          datasets={dsList}
          pickDataset={pickDataset}
          periodLabel={res?.period?.label}
        />
        <section aria-label="Preview" aria-busy={loading} className="min-w-0 space-y-3">
          <div>
            <p className="text-[14px] font-medium">
              {sentence}
              {res?.period && <span className="text-muted"> · {res.period.label}</span>}
            </p>
            {res?.period && (
              <p className="text-[12px] text-muted">
                {fmtDate(res.period.from)} to {fmtDate(res.period.to)}
              </p>
            )}
            {!!spec.filters?.length && (
              <p className="text-[12px] text-muted">
                Filters:{' '}
                {spec.filters
                  .filter(filterReady)
                  .map((f) =>
                    filterText(
                      f,
                      ds.fields.find((x) => x.key === f.field),
                    ),
                  )
                  .join('; ') || 'none'}
              </p>
            )}
          </div>
          <ErrorText error={err} />
          {!res && !err && <div aria-busy="true" className="h-40 animate-pulse rounded-lg bg-hover" />}
          {res && (
            <div className={`space-y-4 ${loading ? 'opacity-60' : ''}`}>
              {loading && <p className="text-[12px] text-muted">Updating…</p>}
              {spec.viz === 'number' && !grouped && <BigNumber res={res} />}
              {spec.viz === 'bar' && grouped && <Bars res={res} spec={spec} ds={ds} onDrill={doDrill} />}
              {res.rows.length === 0 ? (
                <p className="text-[13px] text-muted">No rows match. Widen the period or remove a filter.</p>
              ) : (
                spec.viz !== 'number' && (
                  <DataTable res={res} spec={spec} ds={ds} onDrill={grouped ? doDrill : undefined} />
                )
              )}
              {res.truncated && (
                <p role="status" className="text-[12px] text-warn">
                  Showing first {res.rows.length.toLocaleString('en-IN')} of{' '}
                  {res.count.toLocaleString('en-IN')} rows.
                  {res.totals ? ' Totals include all rows.' : ''} Narrow the filters or export CSV.
                </p>
              )}
              <p className="text-[12px] text-muted">
                {res.count.toLocaleString('en-IN')} matching {res.count === 1 ? 'row' : 'rows'}. Showing only
                what you can access.
                {grouped && ' Click a row to see what is behind it.'}
              </p>
            </div>
          )}
        </section>
      </div>
      {save && (
        <SaveModal
          ws={ws}
          spec={spec}
          report={report}
          defaultName={name || sentence}
          onClose={() => setSave(false)}
          onSaved={(r) => {
            setSave(false)
            setReport(r)
            setName(r.name)
            if (r.id !== id) router.replace(`/w/${ws}/reports/${r.id}`)
          }}
        />
      )}
      {drill && (
        <Drill ws={ws} spec={drill.spec} ds={ds} label={drill.label} onClose={() => setDrill(undefined)} />
      )}
      {del && report && (
        <Confirm
          title="Delete report"
          body={`Delete "${report.name}"? Dashboard tiles using it will show as not available.`}
          action="Delete report"
          onClose={() => setDel(false)}
          onDone={async () => {
            await cmd(ws, 'report.delete', { id: report.id })
            router.push(`/w/${ws}/reports`)
          }}
        />
      )}
    </div>
  )
}
