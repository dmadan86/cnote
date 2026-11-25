'use client'
import { useEffect, useState } from 'react'
import { useShell } from '@/components/shell'
import { ErrorText, Field, inputCls } from '@/components/ui'
import { getTrashRetention, setTrashRetention, type TrashRetention } from '@/lib/docs'

export const RETENTION_CHOICES = [
  { days: 30, label: '30 days' },
  { days: 90, label: '90 days' },
  { days: 365, label: '365 days' },
  { days: 0, label: 'Never delete' },
] as const

/** "Keep deleted pages for": how long Trash keeps pages before they are deleted for good. Admins change it. */
export function TrashSettings({ ws }: { ws: string }) {
  const { me } = useShell()
  const role = me.workspaces.find((w) => w.id === ws)?.role
  const admin = role === 'owner' || role === 'admin'
  const [r, setR] = useState<TrashRetention>()
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  useEffect(() => {
    getTrashRetention(ws).then(setR, (e) => setErr((e as Error).message))
  }, [ws])
  if (!r && !err) return null
  const label = RETENTION_CHOICES.find((c) => c.days === r?.days)?.label ?? `${r?.days} days`
  return (
    <section className="max-w-2xl space-y-2">
      <h2 className="text-[14px] font-semibold">Deleted pages</h2>
      {r && admin ? (
        <div className="max-w-xs">
          <Field label="Keep deleted pages for">
            {(id) => (
              <select
                id={id}
                className={inputCls}
                value={r.days}
                onChange={async (e) => {
                  setErr(undefined)
                  setMsg(undefined)
                  try {
                    const next = await setTrashRetention(ws, Number(e.target.value) as 0 | 30 | 90 | 365)
                    setR(next)
                    setMsg('Saved')
                  } catch (x) {
                    setErr((x as Error).message)
                  }
                }}
              >
                {RETENTION_CHOICES.map((c) => (
                  <option key={c.days} value={c.days}>
                    {c.label}
                  </option>
                ))}
              </select>
            )}
          </Field>
        </div>
      ) : (
        r && <p className="text-[13px]">Deleted pages are kept for: {label}.</p>
      )}
      <p className="text-[13px] text-muted">
        After this time, pages in Trash are deleted for good and cannot be brought back. Changing it gives
        pages already in Trash a full new period from the change. Pages that hold invoices, deals or other
        records are never deleted this way.
      </p>
      <p role="status" className="text-[13px] text-muted">
        {msg}
      </p>
      <ErrorText error={err} />
    </section>
  )
}
