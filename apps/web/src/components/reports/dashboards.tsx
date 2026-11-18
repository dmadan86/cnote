'use client'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useEffect, useState } from 'react'
import { Confirm } from '@/components/billing/bits'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import {
  type Dashboard,
  type DatasetInfo,
  errText,
  fmtTs,
  isDenied,
  type Period,
  PRESETS,
  type Report,
  type RunResult,
  runCmd,
  type Tile,
} from './lib'
import { Bars, BigNumber, DataTable } from './result'

export function DashboardList() {
  const { ws } = useShell()
  const router = useRouter()
  const { data, error } = useCmd<{ dashboards: Dashboard[] }>(ws, 'dashboard.list')
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [err, setErr] = useState<string>()
  const base = `/w/${ws}/reports/dashboards`
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-[15px] font-semibold">Dashboards</h2>
        <button type="button" className={btnPrimary} onClick={() => setOpen(true)}>
          <Plus size={14} aria-hidden /> New dashboard
        </button>
      </div>
      <ErrorText error={error} />
      {data && !data.dashboards.length && (
        <p className="rounded-lg border border-dashed border-line p-6 text-center text-[13px] text-muted">
          No dashboards yet. Create one, then add saved reports to it.
        </p>
      )}
      {!!data?.dashboards.length && (
        <table className="w-full text-left text-[13px]">
          <thead className="text-[12px] text-muted">
            <tr>
              <th className="py-1.5 font-medium">Name</th>
              <th className="font-medium">Reports</th>
              <th className="font-medium">Shared</th>
              <th className="font-medium">Updated</th>
            </tr>
          </thead>
          <tbody>
            {data.dashboards.map((d) => (
              <tr key={d.id} className="border-t border-line hover:bg-hover">
                <td className="py-1.5">
                  <Link href={`${base}/${d.id}`} className="font-medium hover:underline">
                    {d.name}
                  </Link>
                </td>
                <td>{d.tiles.length}</td>
                <td>{d.visibility === 'workspace' ? 'Workspace' : 'Only me'}</td>
                <td>{fmtTs(d.updated_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <Modal open={open} title="New dashboard" onClose={() => setOpen(false)}>
        <form
          className="space-y-3"
          onSubmit={async (e) => {
            e.preventDefault()
            try {
              const { dashboard } = await cmd<{ dashboard: Dashboard }>(ws, 'dashboard.save', {
                name: name.trim(),
                tiles: [],
                visibility: 'private',
              })
              router.push(`${base}/${dashboard.id}`)
            } catch (x) {
              setErr(errText(x))
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
          <ErrorText error={err} />
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setOpen(false)}>
              Cancel
            </button>
            <button type="submit" className={btnPrimary} disabled={!name.trim()}>
              Create dashboard
            </button>
          </div>
        </form>
      </Modal>
    </div>
  )
}

/** One tile runs its own report.run, so a slow or failing tile never blocks the others. */
function TileCard({
  tile,
  report,
  datasets,
  period,
  editing,
  controls,
}: {
  tile: Tile
  report?: Report
  datasets: DatasetInfo[]
  period: Period | null
  editing: boolean
  controls: React.ReactNode
}) {
  const { ws } = useShell()
  const [res, setRes] = useState<RunResult>()
  const [state, setState] = useState<'loading' | 'ok' | 'denied' | 'failed'>('loading')
  const pk = JSON.stringify(period)
  // biome-ignore lint/correctness/useExhaustiveDependencies: pk stands in for period
  useEffect(() => {
    if (!report || tile.available === false) {
      setState('denied')
      return
    }
    const ac = new AbortController()
    runCmd<RunResult>(ws, 'report.run', { id: report.id, ...(period && { period }) }, ac.signal)
      .then((r) => {
        if (r.error) return setState('denied')
        setRes(r)
        setState('ok')
      })
      .catch((e) => !ac.signal.aborted && setState(isDenied(e) ? 'denied' : 'failed'))
    return () => ac.abort()
  }, [ws, report?.id, pk])
  const ds = datasets.find((d) => d.key === report?.dataset)
  const spec = report?.spec
  const viz = tile.size === 'number' ? 'number' : (spec?.viz ?? 'table')
  const span = tile.size === 'number' ? '' : tile.size === 'half' ? 'sm:col-span-2' : 'sm:col-span-4'
  return (
    <section
      aria-label={report?.name ?? tile.name ?? 'Report'}
      className={`min-w-0 rounded-lg border border-line p-3 ${span}`}
    >
      <div className="mb-2 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="truncate text-[12px] font-medium text-muted">
            {report?.name ?? tile.name ?? 'Report'}
          </h3>
          {state === 'ok' && res?.period && <p className="text-[12px] text-muted">{res.period.label}</p>}
        </div>
        {editing
          ? controls
          : report && (
              <Link
                href={`/w/${ws}/reports/${report.id}`}
                className="shrink-0 text-[12px] text-accent hover:underline"
              >
                Open
              </Link>
            )}
      </div>
      {state === 'loading' && <div aria-busy="true" className="h-12 animate-pulse rounded bg-hover" />}
      {state === 'denied' && <p className="text-[13px] text-muted">Not available to you</p>}
      {state === 'failed' && (
        <p role="alert" className="text-[13px] text-bad">
          Could not load this report.
        </p>
      )}
      {state === 'ok' && res && spec && ds && (
        <>
          {viz === 'number' && <BigNumber res={res} />}
          {viz === 'bar' && <Bars res={res} spec={spec} ds={ds} max={tile.size === 'full' ? 12 : 6} />}
          {viz === 'table' && <DataTable res={res} spec={spec} ds={ds} max={tile.size === 'full' ? 15 : 6} />}
        </>
      )}
    </section>
  )
}

export function DashboardView({ id }: { id: string }) {
  const { ws } = useShell()
  const router = useRouter()
  const dash = useCmd<{ dashboard: Dashboard }>(ws, 'dashboard.get', { id })
  const reports = useCmd<{ reports: Report[] }>(ws, 'report.list')
  const sets = useCmd<{ datasets: DatasetInfo[] }>(ws, 'report.datasets')
  const [tiles, setTiles] = useState<Tile[]>()
  const [period, setPeriod] = useState<Period | null>(null)
  const [editing, setEditing] = useState(false)
  const [adding, setAdding] = useState(false)
  const [del, setDel] = useState(false)
  const [err, setErr] = useState<string>()
  const d = dash.data?.dashboard
  useEffect(() => {
    if (d) {
      setTiles(d.tiles)
      setPeriod(d.period)
    }
  }, [d])
  if (dash.error) return <ErrorText error={dash.error} />
  if (!d || !tiles) return <div aria-busy="true" className="h-32 animate-pulse rounded-lg bg-hover" />
  const byId = new Map((reports.data?.reports ?? []).map((r) => [r.id, r]))
  const move = (i: number, by: number) => {
    const t = [...tiles]
    ;[t[i], t[i + by]] = [t[i + by], t[i]]
    setTiles(t)
  }
  const persist = async (t: Tile[], p: Period | null) => {
    setErr(undefined)
    try {
      await cmd(ws, 'dashboard.save', {
        id: d.id,
        name: d.name,
        tiles: t,
        period: p,
        visibility: d.visibility,
      })
      dash.reload()
      return true
    } catch (x) {
      setErr(errText(x))
      return false
    }
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Link href={`/w/${ws}/reports/dashboards`} className="text-[13px] text-muted hover:text-fg">
          Dashboards /
        </Link>
        <h2 className="text-xl font-semibold">{d.name}</h2>
        <Pill>{d.visibility === 'workspace' ? 'Workspace' : 'Private'}</Pill>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-1.5 text-[13px]">
            <span className="text-muted">Period</span>
            <select
              className={`${inputCls} w-auto`}
              value={period?.preset ?? ''}
              onChange={(e) => setPeriod(e.target.value ? { field: '', preset: e.target.value } : null)}
            >
              <option value="">Use each report's period</option>
              {PRESETS.filter(([k]) => k !== 'custom').map(([k, l]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
          </label>
          {d.can_edit &&
            (editing ? (
              <>
                <button type="button" className={btn} onClick={() => setAdding(true)}>
                  <Plus size={14} aria-hidden /> Add report
                </button>
                <button type="button" className={btn} onClick={() => setDel(true)}>
                  <Trash2 size={14} aria-hidden /> Delete
                </button>
                <button
                  type="button"
                  className={btnPrimary}
                  onClick={async () => (await persist(tiles, period)) && setEditing(false)}
                >
                  Done
                </button>
              </>
            ) : (
              <button type="button" className={btn} onClick={() => setEditing(true)}>
                Edit
              </button>
            ))}
        </div>
      </div>
      <ErrorText error={err ?? reports.error ?? sets.error} />
      {!tiles.length && (
        <p className="rounded-lg border border-dashed border-line p-6 text-center text-[13px] text-muted">
          This dashboard is empty. {d.can_edit ? 'Choose Edit, then Add report.' : ''}
        </p>
      )}
      {sets.data && reports.data && (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
          {tiles.map((t, i) => (
            <TileCard
              // biome-ignore lint/suspicious/noArrayIndexKey: the same report may be on twice
              key={`${t.report_id}-${i}`}
              tile={t}
              report={byId.get(t.report_id)}
              datasets={sets.data?.datasets ?? []}
              period={period}
              editing={editing}
              controls={
                <div className="flex shrink-0 items-center gap-0.5">
                  <button
                    type="button"
                    aria-label="Move up"
                    disabled={i === 0}
                    className="rounded p-1 hover:bg-hover disabled:opacity-30"
                    onClick={() => move(i, -1)}
                  >
                    <ArrowUp size={14} aria-hidden />
                  </button>
                  <button
                    type="button"
                    aria-label="Move down"
                    disabled={i === tiles.length - 1}
                    className="rounded p-1 hover:bg-hover disabled:opacity-30"
                    onClick={() => move(i, 1)}
                  >
                    <ArrowDown size={14} aria-hidden />
                  </button>
                  <select
                    aria-label="Tile size"
                    className="rounded border border-line bg-canvas px-1 py-0.5 text-[12px]"
                    value={t.size}
                    onChange={(e) =>
                      setTiles(
                        tiles.map((x, j) => (j === i ? { ...x, size: e.target.value as Tile['size'] } : x)),
                      )
                    }
                  >
                    <option value="number">Number</option>
                    <option value="half">Half width</option>
                    <option value="full">Full width</option>
                  </select>
                  <button
                    type="button"
                    className="rounded px-1.5 py-0.5 text-[12px] hover:bg-hover"
                    onClick={() => setTiles(tiles.filter((_, j) => j !== i))}
                  >
                    Remove
                  </button>
                </div>
              }
            />
          ))}
        </div>
      )}
      <Modal open={adding} title="Add report" onClose={() => setAdding(false)}>
        {!reports.data?.reports.length && (
          <p className="text-[13px] text-muted">No saved reports yet. Build one first.</p>
        )}
        <ul className="max-h-80 space-y-1 overflow-auto">
          {reports.data?.reports.map((r) => (
            <li key={r.id}>
              <button
                type="button"
                className="block w-full rounded-md p-2 text-left hover:bg-hover"
                onClick={() => {
                  setTiles([...tiles, { report_id: r.id, size: r.spec.viz === 'number' ? 'number' : 'half' }])
                  setAdding(false)
                }}
              >
                <span className="block text-[13px] font-medium">{r.name}</span>
                <span className="block text-[12px] text-muted">
                  {sets.data?.datasets.find((x) => x.key === r.dataset)?.label ?? r.dataset} ·{' '}
                  {r.spec.viz ?? 'table'}
                </span>
              </button>
            </li>
          ))}
        </ul>
      </Modal>
      {del && (
        <Confirm
          title="Delete dashboard"
          body={`Delete "${d.name}"? The reports on it stay.`}
          action="Delete dashboard"
          onClose={() => setDel(false)}
          onDone={async () => {
            await cmd(ws, 'dashboard.delete', { id: d.id })
            router.push(`/w/${ws}/reports/dashboards`)
          }}
        />
      )}
    </div>
  )
}
