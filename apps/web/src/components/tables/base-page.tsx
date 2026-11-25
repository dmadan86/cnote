'use client'
import { ChevronLeft, FileUp, Plus, Settings2, Table2 } from 'lucide-react'
import Link from 'next/link'
import { usePathname, useRouter, useSearchParams } from 'next/navigation'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, EmptyState, ErrorText, inputCls, Field as Labelled, Modal } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import {
  type BaseDetail,
  createTable,
  createView,
  deleteTable,
  deleteView,
  getRecord,
  type LinkChip,
  type Table,
  type TableView,
  updateField,
  updateRecord,
  updateTable,
  updateView,
  type ViewConfig,
} from '@/lib/tables'
import { FieldDialog } from './field-dialog'
import { opsFor } from './filter-menu'
import { TableGrid } from './grid'
import { ImportWizard } from './import'
import { Kanban } from './kanban'
import { LinkPicker } from './link-picker'
import { RecordPanel } from './record-panel'
import { ViewBar } from './view-bar'

/** One text box in a dialog, with the save error shown inside it. */
function NameModal({
  title,
  label,
  initial = '',
  submit,
  onSubmit,
  onClose,
  children,
}: {
  title: string
  label: string
  initial?: string
  submit: string
  onSubmit: (name: string) => Promise<void>
  onClose: () => void
  children?: React.ReactNode
}) {
  const [name, setName] = useState(initial)
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  return (
    <Modal open onClose={onClose} title={title}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          if (!name.trim()) return setErr('Give it a name.')
          setBusy(true)
          try {
            await onSubmit(name.trim())
            onClose()
          } catch (x) {
            setErr((x as Error).message)
            setBusy(false)
          }
        }}
      >
        <Labelled label={label}>
          {(id) => (
            <input id={id} value={name} onChange={(e) => setName(e.target.value)} className={inputCls} />
          )}
        </Labelled>
        {children}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btn}>
            Cancel
          </button>
          <button type="submit" disabled={busy} className={btnPrimary}>
            {submit}
          </button>
        </div>
      </form>
    </Modal>
  )
}

type Dlg =
  | { k: 'table' }
  | { k: 'rename-table' }
  | { k: 'view'; type: 'grid' | 'kanban' }
  | { k: 'edit-view' }
  | { k: 'field'; fieldId?: string }
  | { k: 'import' }
  | { k: 'links'; recordId: string; fieldId: string; chips: LinkChip[] }
type Menu = { fieldId: string; x: number; y: number }

export function BasePage({ baseId }: { baseId: string }) {
  const { ws } = useShell()
  const router = useRouter()
  const path = usePathname()
  const sp = useSearchParams()
  const detail = useCmd<BaseDetail>(ws, 'base.get', { id: baseId })
  const [dlg, setDlg] = useState<Dlg>()
  const [menu, setMenu] = useState<Menu>()
  const [gridKey, setGridKey] = useState(0)
  const [err, setErr] = useState<string>()
  const [local, setLocal] = useState<Record<string, ViewConfig>>({}) // optimistic shared-view edits, by view id
  const timers = useRef(new Map<string, { t: ReturnType<typeof setTimeout>; config: ViewConfig }>()) // pending saves, one per view id
  const wantImport = sp.get('import') === '1'

  const setParams = useCallback(
    (p: Record<string, string | null>) => {
      const q = new URLSearchParams(sp.toString())
      for (const [k, v] of Object.entries(p)) v === null ? q.delete(k) : q.set(k, v)
      const s = q.toString()
      router.replace(s ? `${path}?${s}` : path, { scroll: false })
    },
    [sp, path, router],
  )

  const d = detail.data
  const role = d?.role ?? 'read'
  const write = role !== 'read'
  const manager = role === 'manage'
  const tables = d?.tables ?? []
  const table: Table | undefined = tables.find((t) => t.id === sp.get('t')) ?? tables[0]
  const rawView = table?.views.find((v) => v.id === sp.get('v')) ?? table?.views[0]
  const view: TableView | undefined = rawView && { ...rawView, config: local[rawView.id] ?? rawView.config }
  const recordId = sp.get('r')

  // Shared-view edits show at once and are saved 400 ms after the last change.
  const patchView = (patch: Partial<ViewConfig>) => {
    if (!view) return
    const config = { ...view.config, ...patch }
    setLocal((l) => ({ ...l, [view.id]: config }))
    const id = view.id
    clearTimeout(timers.current.get(id)?.t)
    timers.current.set(id, { t: setTimeout(() => flush(id), 400), config })
  }
  const saveView = async (id: string, config: ViewConfig) => {
    try {
      await updateView(ws, id, { config })
      setErr(undefined)
      detail.reload()
    } catch (x) {
      setErr((x as Error).message)
      setLocal((l) => {
        const { [id]: _drop, ...rest } = l
        return rest
      })
    }
  }
  // A view's pending save runs at once when the view is left or the page goes away, never dropped or sent to another view.
  const saveRef = useRef(saveView)
  saveRef.current = saveView
  const flush = useCallback((id: string) => {
    const p = timers.current.get(id)
    if (!p) return
    clearTimeout(p.t)
    timers.current.delete(id)
    void saveRef.current(id, p.config)
  }, [])
  const viewId = view?.id
  useEffect(
    () => () => {
      if (viewId) flush(viewId)
    },
    [viewId, flush],
  )
  useEffect(() => {
    const all = timers.current
    return () => {
      for (const id of [...all.keys()]) flush(id)
    }
  }, [flush])

  // ?import=1 (New base > Import a CSV) opens the wizard once the table exists.
  useEffect(() => {
    if (wantImport && write && table) setDlg({ k: 'import' })
  }, [wantImport, write, table])

  async function addTable(name: string) {
    const t = await createTable(ws, baseId, name)
    detail.reload()
    setParams({ t: t.id, v: null, r: null })
  }
  async function addView(name: string, type: 'grid' | 'kanban') {
    if (!table) return
    const stack = table.fields.find((f) => f.type === 'single_select')
    const v = await createView(
      ws,
      table.id,
      name,
      type,
      type === 'kanban' && stack ? { kanban: { stack: stack.id, card_fields: [] } } : undefined,
    )
    detail.reload()
    setParams({ v: v.id, r: null })
  }
  const onFieldMenu = (fieldId: string, el: HTMLElement) => {
    if (!write) return
    const r = el.getBoundingClientRect()
    setMenu({ fieldId, x: Math.min(r.left, window.innerWidth - 220), y: r.bottom })
  }

  if (detail.error) return <p className="p-6 text-bad">{detail.error.message}</p>
  if (!d) return <p className="p-6 text-muted">Loading base.</p>
  const fields = table?.fields ?? []
  const tableLimit = d.usage.tables >= d.limits.tables_per_base
  const menuField = fields.find((f) => f.id === menu?.fieldId)
  const editField = dlg?.k === 'field' ? fields.find((f) => f.id === dlg.fieldId) : undefined

  return (
    <div className="flex min-h-full flex-col px-6 py-5">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Link href={`/w/${ws}/tables`} aria-label="All bases" className="rounded p-1 hover:bg-hover">
          <ChevronLeft size={16} />
        </Link>
        <h1 className="text-xl font-semibold">{d.base.name}</h1>
        {!write && <span className="text-[12px] text-muted">View only</span>}
      </div>

      {/* Table tabs */}
      <div
        role="tablist"
        aria-label="Tables"
        className="flex flex-wrap items-center gap-1 border-b border-line"
      >
        {tables.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={t.id === table?.id}
            onClick={() => setParams({ t: t.id, v: null, r: null })}
            className={`-mb-px border-b-2 px-3 py-1.5 text-[13px] ${t.id === table?.id ? 'border-accent font-medium' : 'border-transparent text-muted hover:text-fg'}`}
          >
            {t.name}
          </button>
        ))}
        {write && table && (
          <button
            type="button"
            aria-label="Rename table"
            onClick={() => setDlg({ k: 'rename-table' })}
            className="rounded p-1 text-muted hover:bg-hover"
          >
            <Settings2 size={14} />
          </button>
        )}
        {write && (
          <button
            type="button"
            disabled={tableLimit}
            title={tableLimit ? 'This base is at its table limit' : undefined}
            onClick={() => setDlg({ k: 'table' })}
            className={`${btn} border-0`}
          >
            <Plus size={14} /> Add table
          </button>
        )}
      </div>

      <ErrorText error={err} />

      {!table || !view ? (
        <EmptyState
          icon={Table2}
          title="No tables yet"
          action={
            write && (
              <button type="button" onClick={() => setDlg({ k: 'table' })} className={btnPrimary}>
                Add a table
              </button>
            )
          }
        >
          {write
            ? 'Add a table, then add fields and records, or import a CSV.'
            : 'This base has no tables yet.'}
        </EmptyState>
      ) : (
        <>
          {/* View switcher */}
          <div role="tablist" aria-label="Views" className="mt-3 flex flex-wrap items-center gap-1">
            {table.views.map((v) => (
              <button
                key={v.id}
                type="button"
                role="tab"
                aria-selected={v.id === view.id}
                onClick={() => setParams({ v: v.id, r: null })}
                className={`rounded-md px-2.5 py-1 text-[13px] ${v.id === view.id ? 'bg-hover font-medium' : 'text-muted hover:bg-hover'}`}
              >
                {v.type === 'kanban' ? 'Kanban' : 'Grid'}: {v.name}
              </button>
            ))}
            {write && (
              <>
                <button
                  type="button"
                  aria-label="Edit view"
                  onClick={() => setDlg({ k: 'edit-view' })}
                  className="rounded p-1 text-muted hover:bg-hover"
                >
                  <Settings2 size={14} />
                </button>
                <button type="button" onClick={() => setDlg({ k: 'view', type: 'grid' })} className={btn}>
                  <Plus size={14} /> Grid view
                </button>
                <button type="button" onClick={() => setDlg({ k: 'view', type: 'kanban' })} className={btn}>
                  <Plus size={14} /> Kanban view
                </button>
              </>
            )}
          </div>

          <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
            {write ? <ViewBar table={table} fields={fields} view={view} onChange={patchView} /> : <span />}
            <div className="flex items-center gap-2">
              {write && (
                <button type="button" onClick={() => setDlg({ k: 'field' })} className={btn}>
                  <Plus size={14} /> Add field
                </button>
              )}
              {write && (
                <button type="button" onClick={() => setDlg({ k: 'import' })} className={btn}>
                  <FileUp size={14} /> Import CSV
                </button>
              )}
            </div>
          </div>

          <div className="mt-3 h-[calc(100vh-14rem)] min-h-96">
            {view.type === 'kanban' ? (
              <Kanban
                ws={ws}
                table={table}
                fields={fields}
                view={view}
                role={role}
                refresh={gridKey}
                onOpenRecord={(id) => setParams({ r: id })}
              />
            ) : (
              <TableGrid
                key={`${view.id}:${gridKey}`}
                table={table}
                fields={fields}
                view={view}
                role={role}
                onOpenRecord={(id) => setParams({ r: id })}
                onFieldMenu={onFieldMenu}
                onAddField={write ? () => setDlg({ k: 'field' }) : undefined}
                onPickLinks={
                  write
                    ? async (recordId, fieldId) => {
                        try {
                          const r = await getRecord(ws, recordId)
                          setDlg({ k: 'links', recordId, fieldId, chips: r.links[fieldId] ?? [] })
                        } catch (x) {
                          setErr((x as Error).message)
                        }
                      }
                    : undefined
                }
              />
            )}
          </div>
        </>
      )}

      {/* Field header menu (opened by the grid) */}
      {menu && menuField && table && view && (
        <FieldMenu
          x={menu.x}
          y={menu.y}
          field={menuField}
          manager={manager}
          onClose={() => setMenu(undefined)}
          onEdit={() => setDlg({ k: 'field', fieldId: menuField.id })}
          onSort={(desc) => patchView({ sorts: [{ field: menuField.id, desc }] })}
          onFilter={() =>
            patchView({
              filters: {
                match: view.config.filters?.match ?? 'and',
                rules: [
                  ...(view.config.filters?.rules ?? []),
                  { field: menuField.id, op: opsFor(menuField.type)[0] },
                ].slice(0, 10),
              },
            })
          }
          onLock={async () => {
            try {
              await updateField(ws, menuField.id, { locked: !menuField.locked })
              detail.reload()
            } catch (x) {
              setErr((x as Error).message)
            }
          }}
        />
      )}

      {dlg?.k === 'table' && (
        <NameModal
          title="Add table"
          label="Table name"
          submit="Add table"
          onSubmit={addTable}
          onClose={() => setDlg(undefined)}
        />
      )}
      {dlg?.k === 'rename-table' && table && (
        <RenameTable
          table={table}
          manager={manager}
          onRename={async (n) => {
            await updateTable(ws, table.id, { name: n })
            detail.reload()
          }}
          onDelete={async () => {
            await deleteTable(ws, table.id)
            detail.reload()
            setParams({ t: null, v: null, r: null })
          }}
          onClose={() => setDlg(undefined)}
        />
      )}
      {dlg?.k === 'view' && (
        <NameModal
          title={dlg.type === 'kanban' ? 'New kanban view' : 'New grid view'}
          label="View name"
          initial={dlg.type === 'kanban' ? 'Board' : ''}
          submit="Create view"
          onSubmit={(n) => addView(n, dlg.type)}
          onClose={() => setDlg(undefined)}
        />
      )}
      {dlg?.k === 'edit-view' && view && table && (
        <EditView
          view={view}
          canDelete={table.views.filter((v) => v.type === 'grid').length > 1 || view.type !== 'grid'}
          onRename={async (n) => {
            await updateView(ws, view.id, { name: n })
            detail.reload()
          }}
          onDelete={async () => {
            await deleteView(ws, view.id)
            detail.reload()
            setParams({ v: null })
          }}
          onClose={() => setDlg(undefined)}
        />
      )}
      {dlg?.k === 'field' && table && (
        <FieldDialog
          ws={ws}
          table={table}
          tables={tables}
          field={editField}
          role={role}
          onClose={() => setDlg(undefined)}
          onSaved={() => {
            detail.reload()
            setGridKey((k) => k + 1)
          }}
        />
      )}
      {dlg?.k === 'import' && table && (
        <ImportWizard
          ws={ws}
          baseId={baseId}
          table={table}
          fields={fields}
          onClose={() => {
            setDlg(undefined)
            if (wantImport) setParams({ import: null })
          }}
          onDone={() => {
            detail.reload()
            setGridKey((k) => k + 1)
          }}
        />
      )}
      {dlg?.k === 'links' && table && (
        <CellLinks
          ws={ws}
          tables={tables}
          field={fields.find((f) => f.id === dlg.fieldId)}
          chips={dlg.chips}
          recordId={dlg.recordId}
          onSaved={() => setGridKey((k) => k + 1)}
          onClose={() => setDlg(undefined)}
        />
      )}

      {recordId && table && (
        <RecordPanel
          key={recordId}
          ws={ws}
          table={table}
          fields={fields}
          tables={tables}
          hidden={view?.config.hidden ?? []}
          recordId={recordId}
          role={role}
          onClose={() => setParams({ r: null })}
          onChanged={() => setGridKey((k) => k + 1)}
        />
      )}
    </div>
  )
}

function RenameTable({
  table,
  manager,
  onRename,
  onDelete,
  onClose,
}: {
  table: Table
  manager: boolean
  onRename: (n: string) => Promise<void>
  onDelete: () => Promise<void>
  onClose: () => void
}) {
  const [sure, setSure] = useState(false)
  const [err, setErr] = useState<string>()
  return (
    <NameModal
      title="Rename table"
      label="Table name"
      initial={table.name}
      submit="Save"
      onSubmit={onRename}
      onClose={onClose}
    >
      {manager &&
        (sure ? (
          <div
            role="alertdialog"
            aria-label="Delete this table"
            className="space-y-2 rounded-md border border-bad p-2"
          >
            <p className="text-[13px]">
              Delete {table.name} and its records? You can restore it for 30 days.
            </p>
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={() => setSure(false)}>
                Keep table
              </button>
              <button
                type="button"
                className={`${btn} text-bad`}
                onClick={async () => {
                  try {
                    await onDelete()
                    onClose()
                  } catch (x) {
                    setErr((x as Error).message)
                  }
                }}
              >
                Delete table
              </button>
            </div>
            <ErrorText error={err} />
          </div>
        ) : (
          <button type="button" className={`${btn} text-bad`} onClick={() => setSure(true)}>
            Delete table
          </button>
        ))}
    </NameModal>
  )
}

function EditView({
  view,
  canDelete,
  onRename,
  onDelete,
  onClose,
}: {
  view: TableView
  canDelete: boolean
  onRename: (n: string) => Promise<void>
  onDelete: () => Promise<void>
  onClose: () => void
}) {
  const [err, setErr] = useState<string>()
  return (
    <NameModal
      title="Edit view"
      label="View name"
      initial={view.name}
      submit="Save"
      onSubmit={onRename}
      onClose={onClose}
    >
      {canDelete && (
        <button
          type="button"
          className={`${btn} text-bad`}
          onClick={async () => {
            try {
              await onDelete()
              onClose()
            } catch (x) {
              setErr((x as Error).message)
            }
          }}
        >
          Delete view
        </button>
      )}
      <ErrorText error={err} />
    </NameModal>
  )
}

function CellLinks({
  ws,
  tables,
  field,
  chips,
  recordId,
  onSaved,
  onClose,
}: {
  ws: string
  tables: Table[]
  field?: { id: string; config: { table_id?: string; multiple?: boolean } }
  chips: LinkChip[]
  recordId: string
  onSaved: () => void
  onClose: () => void
}) {
  const target = tables.find((t) => t.id === field?.config.table_id)
  if (!field || !target) return null
  return (
    <LinkPicker
      ws={ws}
      target={target}
      value={chips}
      multiple={!!field.config.multiple}
      canCreate
      onDone={async (c) => {
        await updateRecord(ws, recordId, { links: { [field.id]: c.map((x) => x.id) } })
        onSaved()
      }}
      onClose={onClose}
    />
  )
}

function FieldMenu({
  x,
  y,
  field,
  manager,
  onClose,
  onEdit,
  onSort,
  onFilter,
  onLock,
}: {
  x: number
  y: number
  field: { name: string; locked: boolean }
  manager: boolean
  onClose: () => void
  onEdit: () => void
  onSort: (desc: boolean) => void
  onFilter: () => void
  onLock: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    ref.current?.querySelector<HTMLButtonElement>('button')?.focus()
    const down = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && onClose()
    const key = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    document.addEventListener('mousedown', down)
    document.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('mousedown', down)
      document.removeEventListener('keydown', key)
    }
  }, [onClose])
  const item = (label: string, fn: () => void) => (
    <button
      type="button"
      role="menuitem"
      onClick={() => {
        fn()
        onClose()
      }}
      className="block w-full rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover"
    >
      {label}
    </button>
  )
  return (
    <div
      ref={ref}
      role="menu"
      aria-label={`${field.name} field`}
      style={{ left: x, top: y }}
      className="fixed z-40 w-52 rounded-lg border border-line bg-canvas p-1 shadow-xl"
    >
      {item('Edit field', onEdit)}
      {item('Sort A to Z', () => onSort(false))}
      {item('Sort Z to A', () => onSort(true))}
      {item('Filter by this field', onFilter)}
      {manager && item(field.locked ? 'Unlock field' : 'Lock field', onLock)}
    </div>
  )
}
