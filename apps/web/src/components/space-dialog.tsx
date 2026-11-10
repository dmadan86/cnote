'use client'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { cmd, type Space } from '@/lib/api'
import { useShell } from './shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from './ui'

export function SpaceDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { ws, reload } = useShell()
  const router = useRouter()
  const [error, setError] = useState<string>()

  async function submit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    try {
      const s = await cmd<Space>(ws, 'space.create', {
        key: f.get('key'),
        name: f.get('name'),
        template: f.get('template'),
        visibility: f.get('visibility'),
      })
      setError(undefined)
      reload()
      onClose()
      router.push(`/w/${ws}/s/${s.id}`)
    } catch (err) {
      setError((err as Error).message)
    }
  }

  return (
    <Modal open={open} onClose={onClose} title="Create space">
      <form onSubmit={submit} className="space-y-3">
        <Field label="Name">{(id) => <input id={id} name="name" required className={inputCls} />}</Field>
        <Field label="Key">
          {(id) => (
            <input
              id={id}
              name="key"
              required
              maxLength={10}
              placeholder="ENG"
              className={`${inputCls} uppercase`}
            />
          )}
        </Field>
        <Field label="Template">
          {(id) => (
            <select id={id} name="template" defaultValue="docs" className={inputCls}>
              {['blank', 'docs', 'software', 'kanban', 'crm', 'billing'].map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Visibility">
          {(id) => (
            <select id={id} name="visibility" defaultValue="open" className={inputCls}>
              <option value="open">Open to the workspace</option>
              <option value="private">Private</option>
            </select>
          )}
        </Field>
        <ErrorText error={error} />
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btn}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Create space
          </button>
        </div>
      </form>
    </Modal>
  )
}
