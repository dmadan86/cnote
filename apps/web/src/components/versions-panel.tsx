'use client'
import { useState } from 'react'
import { btn, btnPrimary, ErrorText } from '@/components/ui'
import { cmd, type NoteDetail } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { personProps } from './person'
import { VersionCompare } from './version-compare'

type Version = {
  id: string
  number: number
  title: string
  reason: 'edit' | 'before_restore' | 'restore'
  restored_from: string | null
  created_by: string | null
  created_by_name: string | null
  created_at: string
  content_md?: string
}

const LABEL: Record<Version['reason'], string> = {
  edit: 'Saved while editing',
  before_restore: 'Kept before a restore',
  restore: 'Restored',
}

/** Saved versions of a page; any of them can be restored (which itself becomes a new version). */
export function VersionsTab({
  ws,
  note,
  canEdit,
  onRestored,
}: {
  ws: string
  note: NoteDetail
  canEdit: boolean
  onRestored: () => void
}) {
  const list = useCmd<Version[]>(ws, 'note.revisions', { note_id: note.id })
  const [open, setOpen] = useState<Version>()
  const [confirm, setConfirm] = useState(false)
  const [err, setErr] = useState<string>()
  const [sel, setSel] = useState<string[]>([]) // up to two versions chosen for comparing
  const [cmp, setCmp] = useState<{ from: string; to: string; fromLabel: string; toLabel: string }>()

  const pick = (id: string) => {
    setCmp(undefined)
    setSel((l) => (l.includes(id) ? l.filter((x) => x !== id) : [...l, id].slice(-2)))
  }
  // Two versions compare older to newer; one version compares with the page as it is now. Needs read access only.
  async function compare() {
    setErr(undefined)
    try {
      const vs = (await Promise.all(sel.map((id) => cmd<Version>(ws, 'note.revision', { id })))).sort(
        (a, b) => a.number - b.number,
      )
      const [a, b] = vs
      setCmp({
        from: a.content_md ?? '',
        fromLabel: `version ${a.number}`,
        to: b ? (b.content_md ?? '') : note.content_md,
        toLabel: b ? `version ${b.number}` : 'the current page',
      })
    } catch (x) {
      setErr((x as Error).message)
    }
  }

  async function show(v: Version) {
    setConfirm(false)
    setErr(undefined)
    if (open?.id === v.id) return setOpen(undefined)
    setOpen(await cmd<Version>(ws, 'note.revision', { id: v.id }))
  }

  async function restore(v: Version) {
    try {
      await cmd(ws, 'note.restore', {
        note_id: note.id,
        revision_id: v.id,
        expected_epoch: note.collab_epoch,
      })
      setOpen(undefined)
      list.reload()
      onRestored()
    } catch (x) {
      setErr((x as Error).message)
    }
  }

  if (list.data?.length === 0)
    return (
      <p className="text-[13px] text-muted">
        No saved versions yet. Versions are kept as the page is edited.
      </p>
    )
  return (
    <>
      <div className="mb-2 flex flex-wrap items-center gap-2 text-[12px] text-muted">
        <button type="button" className={btn} disabled={sel.length === 0} onClick={compare}>
          {sel.length === 2 ? 'Compare selected versions' : 'Compare with current page'}
        </button>
        <span>Tick one version to compare it with the current page, or two to compare them.</span>
      </div>
      {cmp && <VersionCompare {...cmp} />}
      {!cmp && <ErrorText error={err} />}
      <ul aria-label="Versions" className="space-y-1.5">
        {list.data?.map((v) => (
          <li key={v.id} className="flex items-start gap-2 rounded-md border border-line pl-2 text-[13px]">
            <input
              type="checkbox"
              aria-label={`Select version ${v.number} to compare`}
              checked={sel.includes(v.id)}
              onChange={() => pick(v.id)}
              className="mt-3"
            />
            <div className="min-w-0 flex-1">
              <button
                type="button"
                aria-expanded={open?.id === v.id}
                onClick={() => show(v)}
                className="flex w-full items-center justify-between gap-2 px-2.5 py-2 text-left hover:bg-hover"
              >
                <span>
                  <span className="font-medium">Version {v.number}</span>
                  <span className="block text-[12px] text-muted">
                    {LABEL[v.reason]} ·{' '}
                    <span {...personProps(v.created_by)}>{v.created_by_name ?? 'Agent'}</span>
                  </span>
                </span>
                <span className="shrink-0 text-[12px] text-muted">{ago(v.created_at)}</span>
              </button>
              {open?.id === v.id && (
                <div className="space-y-2 border-t border-line p-2.5">
                  <section aria-label={`Version ${v.number} content`}>
                    <pre className="max-h-64 overflow-auto whitespace-pre-wrap font-sans text-[12px]">
                      {open.content_md || '(empty)'}
                    </pre>
                  </section>
                  <ErrorText error={err} />
                  {canEdit &&
                    (confirm ? (
                      <div className="flex gap-2">
                        <button type="button" className={btnPrimary} onClick={() => restore(v)}>
                          Restore version {v.number}
                        </button>
                        <button type="button" className={btn} onClick={() => setConfirm(false)}>
                          Cancel
                        </button>
                      </div>
                    ) : (
                      <button type="button" className={btn} onClick={() => setConfirm(true)}>
                        Restore this version
                      </button>
                    ))}
                  {confirm && (
                    <p className="text-[12px] text-muted">
                      The current page is kept as a version first, and anyone editing it reloads.
                    </p>
                  )}
                </div>
              )}
            </div>
          </li>
        ))}
      </ul>
    </>
  )
}
