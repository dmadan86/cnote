'use client'
import { useState } from 'react'
import { requestAccess } from '@/lib/docs'
import { btn, btnPrimary, ErrorText, inputCls } from '../ui'

/** Shown where a page cannot be opened. It looks the same whether or not the page exists. */
export function RequestAccess({ ws, id }: { ws: string; id: string }) {
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)
  const [err, setErr] = useState<Error>()
  if (sent)
    return (
      <p role="status" className="px-6 text-[13px] text-muted">
        If this page exists, the people who can grant access have been asked. If nobody replies, ask your
        workspace admin for access.
      </p>
    )
  return (
    <form
      className="max-w-md space-y-2 px-6"
      onSubmit={async (e) => {
        e.preventDefault()
        setBusy(true)
        setErr(undefined)
        try {
          await requestAccess(ws, id, msg.trim() || undefined)
          setSent(true)
        } catch (x) {
          setErr(x as Error)
        }
        setBusy(false)
      }}
    >
      <label className="block text-[13px]">
        <span className="text-muted">Message (optional)</span>
        <input
          aria-label="Message to the people who can grant access"
          maxLength={200}
          value={msg}
          onChange={(e) => setMsg(e.target.value)}
          placeholder="Why do you need it?"
          className={`${inputCls} mt-1`}
        />
      </label>
      <ErrorText error={err} />
      <button type="submit" disabled={busy} className={msg ? btnPrimary : btn}>
        Request access
      </button>
    </form>
  )
}
