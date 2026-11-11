'use client'
import { FilePlus2, LayoutTemplate } from 'lucide-react'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { cmd, type Note } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

type Tpl = { id: string; name: string; kind: 'page' | 'board'; source_kind: string }
type Opt = { id: string; label: string }
type Rendered = { note: Note; unresolved: string[] }

const LIST: Record<string, [string, (r: Record<string, string | null>) => string]> = {
  contact: ['contact.list', (r) => r.name ?? ''],
  deal: ['deal.list', (r) => r.title ?? ''],
  invoice: ['invoice.list', (r) => r.number ?? 'Draft'],
  item: ['item.list', (r) => `${r.key} ${r.title}`],
}

/** Records a template of this kind can read from, as {id, label} options. */
function useSources(ws: string, kind: string, enabled: boolean) {
  const [name, label] = LIST[kind] ?? ['contact.list', () => '']
  const rows = useCmd<Record<string, string | null>[]>(ws, name, { limit: 100 }, enabled && kind in LIST)
  return {
    error: rows.error,
    options: rows.data?.map((r): Opt => ({ id: r.id as string, label: label(r) })),
  }
}

/**
 * Pick a template, fill in the record it reads from and render it into a new note.
 * `sourceKind`/`sourceId` pin the record (used from detail pages); otherwise the user picks one.
 */
function TemplateDialog({
  open,
  onClose,
  kind,
  sourceKind,
  sourceId,
}: {
  open: boolean
  onClose: () => void
  kind?: 'page' | 'board'
  sourceKind?: string
  sourceId?: string
}) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const list = useCmd<Tpl[]>(ws, 'template.list', { kind, source_kind: sourceKind }, open)
  const [tid, setTid] = useState('')
  const [src, setSrc] = useState('')
  const [err, setErr] = useState<string>()
  const [result, setResult] = useState<Rendered>()
  const tpl = list.data?.find((t) => t.id === (tid || list.data?.[0]?.id))
  const needsPick = !!tpl && tpl.source_kind !== 'none' && !sourceId
  const sources = useSources(ws, tpl?.source_kind ?? 'none', open && needsPick)

  const open_ = (n: Note) =>
    router.push(
      n.types.some((t) => t === 'whiteboard' || t === 'design')
        ? `/w/${ws}/whiteboard/${n.id}`
        : `/w/${ws}/n/${n.id}`,
    )

  async function submit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!tpl) return
    const f = new FormData(e.currentTarget)
    try {
      const r = await cmd<Rendered>(ws, 'template.render', {
        template_id: tpl.id,
        source_id: sourceId ?? (tpl.source_kind === 'none' ? undefined : src || sources.options?.[0]?.id),
        space_id: tpl.source_kind === 'none' ? f.get('space') : undefined,
        title: f.get('title') || undefined,
      })
      setErr(undefined)
      if (r.unresolved.length === 0) return open_(r.note)
      setResult(r)
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const close = () => {
    setResult(undefined)
    setErr(undefined)
    onClose()
  }

  return (
    <Modal open={open} onClose={close} title="Use a template">
      {result ? (
        <div className="space-y-3">
          <p role="status">Created "{result.note.title}".</p>
          <p className="text-[13px] text-warn">
            These fields had no data and were left as written:{' '}
            {result.unresolved.map((u) => `{{${u}}}`).join(', ')}
          </p>
          <div className="flex justify-end">
            <button type="button" className={btnPrimary} onClick={() => open_(result.note)}>
              Open
            </button>
          </div>
        </div>
      ) : (
        <form className="space-y-3" onSubmit={submit}>
          <ErrorText error={list.error} />
          {list.data?.length === 0 && (
            <p className="text-[13px] text-muted">
              No templates yet. Open a page or board and choose "Save as template".
            </p>
          )}
          <Field label="Template">
            {(id) => (
              <select
                id={id}
                className={inputCls}
                value={tpl?.id ?? ''}
                onChange={(e) => {
                  setTid(e.target.value)
                  setSrc('')
                }}
              >
                {list.data?.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </select>
            )}
          </Field>
          {needsPick && (
            <Field label={`Fill from ${tpl.source_kind}`}>
              {(id) => (
                <select id={id} className={inputCls} value={src} onChange={(e) => setSrc(e.target.value)}>
                  {sources.options?.map((o) => (
                    <option key={o.id} value={o.id}>
                      {o.label}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          )}
          {tpl?.source_kind === 'none' && (
            <Field label="Space">
              {(id) => (
                <select id={id} name="space" className={inputCls}>
                  {spaces.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
              )}
            </Field>
          )}
          <Field label="Title (optional)">
            {(id) => (
              <input id={id} name="title" className={inputCls} placeholder="Defaults to the template title" />
            )}
          </Field>
          <ErrorText error={err ?? sources.error} />
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={close}>
              Cancel
            </button>
            <button type="submit" className={btnPrimary} disabled={!tpl}>
              Create
            </button>
          </div>
        </form>
      )}
    </Modal>
  )
}

/** "Use template" on a contact, deal or invoice detail page. */
export function UseTemplateButton({ sourceKind, sourceId }: { sourceKind: string; sourceId: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className={btn} onClick={() => setOpen(true)}>
        <LayoutTemplate size={14} /> Use template
      </button>
      <TemplateDialog
        open={open}
        onClose={() => setOpen(false)}
        sourceKind={sourceKind}
        sourceId={sourceId}
      />
    </>
  )
}

/** "New from template" on the docs and whiteboard home pages. */
export function NewFromTemplateButton({ kind }: { kind: 'page' | 'board' }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className={btn} onClick={() => setOpen(true)}>
        <FilePlus2 size={14} /> New from template
      </button>
      <TemplateDialog open={open} onClose={() => setOpen(false)} kind={kind} />
    </>
  )
}
