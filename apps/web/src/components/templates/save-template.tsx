'use client'
import { LayoutTemplate } from 'lucide-react'
import { useState } from 'react'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { cmd } from '@/lib/api'
import { SOURCE_KINDS } from './fields'

/** Toolbar button + dialog that saves a page or board as a template (`{{placeholders}}` bind to the chosen record). */
export function SaveAsTemplate({ ws, noteId, title }: { ws: string; noteId: string; title: string }) {
  const [open, setOpen] = useState(false)
  const [err, setErr] = useState<string>()
  const [done, setDone] = useState<string>()

  async function save(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    try {
      await cmd(ws, 'template.save', {
        note_id: noteId,
        name: f.get('name'),
        source_kind: f.get('kind'),
      })
      setErr(undefined)
      setDone(String(f.get('name')))
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  const close = () => {
    setOpen(false)
    setDone(undefined)
    setErr(undefined)
  }

  return (
    <>
      <button type="button" className={btn} onClick={() => setOpen(true)}>
        <LayoutTemplate size={14} /> Save as template
      </button>
      <Modal open={open} onClose={close} title="Save as template">
        {done ? (
          <div className="space-y-3">
            <p role="status">Saved template "{done}".</p>
            <div className="flex justify-end">
              <button type="button" className={btnPrimary} onClick={close}>
                Done
              </button>
            </div>
          </div>
        ) : (
          <form className="space-y-3" onSubmit={save}>
            <p className="text-[13px] text-muted">
              Fields written as {'{{contact.name}}'} are filled from a record when the template is used.
              Saving an existing name replaces that template.
            </p>
            <Field label="Template name">
              {(id) => (
                <input id={id} name="name" required defaultValue={title.trim()} className={inputCls} />
              )}
            </Field>
            <Field label="Fills from">
              {(id) => (
                <select id={id} name="kind" defaultValue="none" className={inputCls}>
                  {SOURCE_KINDS.map(([k, label]) => (
                    <option key={k} value={k}>
                      {label}
                    </option>
                  ))}
                </select>
              )}
            </Field>
            <ErrorText error={err} />
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={close}>
                Cancel
              </button>
              <button type="submit" className={btnPrimary}>
                Save template
              </button>
            </div>
          </form>
        )}
      </Modal>
    </>
  )
}
