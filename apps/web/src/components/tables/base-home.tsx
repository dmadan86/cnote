'use client'
import { Plus, Table2 } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btnPrimary, EmptyState, ErrorText } from '@/components/ui'
import { useCmd, useOpenOnNew } from '@/lib/hooks'
import type { Base } from '@/lib/tables'
import { NewBase, type Start } from './new-base'

const plural = (n: number, w: string) => `${n.toLocaleString('en-IN')} ${w}${n === 1 ? '' : 's'}`

export function BaseHome() {
  const { ws, spaces, me } = useShell()
  const canAdd = spaces.length > 0 && me.workspaces.find((w) => w.id === ws)?.role !== 'guest'
  const bases = useCmd<Base[]>(ws, 'base.list', {})
  const [dlg, setDlg] = useState<Start>()
  const [last, setLast] = useState<Start>('blank')
  const open = (s: Start) => {
    setLast(s)
    setDlg(s)
  }
  const list = bases.data ?? []
  useOpenOnNew(canAdd, () => open('blank'))
  return (
    <div className="px-6 py-6">
      <div className="mb-4 flex items-center justify-between gap-2">
        <h1 className="text-xl font-semibold">Tables</h1>
        <button type="button" onClick={() => open('blank')} className={btnPrimary}>
          <Plus size={14} /> New base
        </button>
      </div>
      <ErrorText error={bases.error} />
      {bases.loading && <p className="text-[13px] text-muted">Loading bases.</p>}
      {bases.data && list.length === 0 && (
        <EmptyState
          icon={Table2}
          title="No tables yet"
          action={
            canAdd ? (
              <>
                <button type="button" onClick={() => open('template')} className={btnPrimary}>
                  Start from a template
                </button>
                <button
                  type="button"
                  onClick={() => open('blank')}
                  className="text-[13px] text-muted underline"
                >
                  Start blank
                </button>
              </>
            ) : undefined
          }
        >
          Keep any list your way: orders, assets, vendors.
        </EmptyState>
      )}
      {list.length > 0 && (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((b) => (
            <li key={b.id}>
              <Link
                href={`/w/${ws}/tables/${b.id}`}
                className="block rounded-lg border border-line p-4 hover:bg-hover"
              >
                <p className="flex items-center gap-2 text-[14px] font-semibold">
                  <Table2 size={16} aria-hidden="true" className="text-muted" />
                  {b.name}
                </p>
                <p className="mt-1 text-[12px] text-muted">
                  {spaces.find((s) => s.id === b.space_id)?.name ?? 'Space'}
                </p>
                <p className="text-[12px] text-muted">
                  {plural(b.table_count, 'table')} · {plural(b.record_count, 'record')}
                </p>
              </Link>
            </li>
          ))}
        </ul>
      )}
      <NewBase open={!!dlg} start={dlg ?? last} onClose={() => setDlg(undefined)} />
    </div>
  )
}
