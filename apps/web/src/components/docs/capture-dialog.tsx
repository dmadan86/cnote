'use client'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { cmd } from '@/lib/api'

/** Paste a transcript, chat or PR text; doc.capture drafts a page from it. */
export function CaptureDialog({
  ws,
  open,
  onClose,
  defaultSpace,
}: {
  ws: string
  open: boolean
  onClose: () => void
  defaultSpace?: string
}) {
  const { spaces } = useShell()
  const router = useRouter()
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  return (
    <Modal open={open} onClose={onClose} title="Capture a page" className="max-w-xl">
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          setBusy(true)
          try {
            const n = await cmd<{ id: string }>(ws, 'doc.capture', {
              source: f.get('source'),
              space_id: f.get('space'),
              text: f.get('text'),
              title: f.get('title') || null,
            })
            onClose()
            router.push(`/w/${ws}/n/${n.id}`)
          } catch (x) {
            setErr((x as Error).message)
          } finally {
            setBusy(false)
          }
        }}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label="Source">
            {(id) => (
              <select id={id} name="source" className={inputCls}>
                <option value="transcript">Meeting transcript</option>
                <option value="chat">Chat thread</option>
                <option value="pr">Pull request</option>
              </select>
            )}
          </Field>
          <Field label="Space">
            {(id) => (
              <select id={id} name="space" defaultValue={defaultSpace ?? spaces[0]?.id} className={inputCls}>
                {spaces.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            )}
          </Field>
        </div>
        <Field label="Title (optional)">{(id) => <input id={id} name="title" className={inputCls} />}</Field>
        <Field label="Text to capture">
          {(id) => <textarea id={id} name="text" required rows={8} className={inputCls} />}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btn}>
            Cancel
          </button>
          <button type="submit" disabled={busy} className={btnPrimary}>
            {busy ? 'Capturing...' : 'Create page'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
