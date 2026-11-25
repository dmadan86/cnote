'use client'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { createBase, type TableTemplate } from '@/lib/tables'

export type Start = 'blank' | 'template' | 'csv'
const STARTS: [Start, string, string][] = [
  ['blank', 'Blank', 'An empty base. Add your own tables.'],
  ['template', 'From a template', 'Start from a ready-made base.'],
  ['csv', 'Import a CSV', 'Bring in a spreadsheet you already have.'],
]

export function NewBase({ open, start, onClose }: { open: boolean; start: Start; onClose: () => void }) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const [mode, setMode] = useState<Start>(start)
  const [name, setName] = useState('')
  const [space, setSpace] = useState('')
  const [tpl, setTpl] = useState('')
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [seen, setSeen] = useState(open)
  // Re-open on the chosen start; the modal itself unmounts its body when closed.
  if (open !== seen) {
    setSeen(open)
    if (open) {
      setMode(start)
      setErr(undefined)
    }
  }
  const templates = useCmd<TableTemplate[]>(ws, 'table_template.list', {}, open && mode === 'template')
  const spaceId = space || spaces[0]?.id || ''

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setErr(undefined)
    if (!name.trim()) return setErr('Give the base a name.')
    if (!spaceId) return setErr('Create a space first. A base lives in a space.')
    if (mode === 'template' && !tpl) return setErr('Choose a template.')
    setBusy(true)
    try {
      const b = await createBase(ws, spaceId, name.trim(), mode === 'template' ? tpl : undefined)
      router.push(`/w/${ws}/tables/${b.id}${mode === 'csv' ? '?import=1' : ''}`)
    } catch (x) {
      setErr((x as Error).message)
      setBusy(false)
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="New base" className="max-w-2xl">
      <form onSubmit={submit} className="space-y-3">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Name">
            {(id) => (
              <input
                id={id}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Orders 2026"
                className={inputCls}
              />
            )}
          </Field>
          <Field label="Space">
            {(id) => (
              <select id={id} value={spaceId} onChange={(e) => setSpace(e.target.value)} className={inputCls}>
                {spaces.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            )}
          </Field>
        </div>
        <fieldset>
          <legend className="mb-1 text-[12px] font-medium text-muted">Start from</legend>
          <div className="grid gap-2 sm:grid-cols-3">
            {STARTS.map(([k, label, hint]) => (
              <label
                key={k}
                className={`cursor-pointer rounded-md border p-2.5 text-[13px] has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent ${mode === k ? 'border-accent bg-hover' : 'border-line'}`}
              >
                <input
                  type="radio"
                  name="start"
                  value={k}
                  checked={mode === k}
                  onChange={() => setMode(k)}
                  className="sr-only"
                />
                <span className="block font-medium">{label}</span>
                <span className="text-[12px] text-muted">{hint}</span>
              </label>
            ))}
          </div>
        </fieldset>
        {mode === 'template' && (
          <fieldset>
            <legend className="mb-1 text-[12px] font-medium text-muted">Template</legend>
            {templates.loading && <p className="text-[13px] text-muted">Loading templates.</p>}
            <ErrorText error={templates.error} />
            <div className="grid max-h-56 gap-2 overflow-auto sm:grid-cols-3">
              {templates.data?.map((t) => (
                <label
                  key={t.key}
                  className={`cursor-pointer rounded-md border p-2.5 text-[13px] has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent ${tpl === t.key ? 'border-accent bg-hover' : 'border-line'}`}
                >
                  <input
                    type="radio"
                    name="template"
                    value={t.key}
                    checked={tpl === t.key}
                    onChange={() => {
                      setTpl(t.key)
                      if (!name) setName(t.name)
                    }}
                    className="sr-only"
                  />
                  <span className="block font-medium">{t.name}</span>
                  <span className="block text-[12px] text-muted">{t.tables.join(', ')}</span>
                </label>
              ))}
            </div>
          </fieldset>
        )}
        {mode === 'csv' && (
          <p className="text-[13px] text-muted">
            The base opens empty, ready for you to choose the CSV file.
          </p>
        )}
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btn}>
            Cancel
          </button>
          <button type="submit" disabled={busy} className={btnPrimary}>
            Create base
          </button>
        </div>
      </form>
    </Modal>
  )
}
