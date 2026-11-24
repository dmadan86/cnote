'use client'
import { useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls } from '@/components/ui'
import { siteApi3b } from '@/lib/sites'

/** What site.get adds when the abuse scan has acted (assumed shape). */
export type AbuseMeta = { case_id?: string; appealed?: boolean; blocked_links?: string[] }

export function AbuseBanner({
  ws,
  takenDown,
  abuse,
}: {
  ws: string
  takenDown?: { reason?: string } | null
  abuse?: AbuseMeta
}) {
  const [open, setOpen] = useState(false)
  const [note, setNote] = useState('')
  const [sent, setSent] = useState(!!abuse?.appealed)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error | null>(null)
  const links = abuse?.blocked_links ?? []
  if (!takenDown && !links.length) return null
  const appeal = async () => {
    if (!abuse?.case_id) return
    setBusy(true)
    try {
      await siteApi3b.abuseAppeal(ws, abuse.case_id, note.trim())
      setSent(true)
      setOpen(false)
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div role="alert" className="space-y-2 border-b border-line bg-bad-bg px-3 py-2 text-[13px] text-bad">
      {takenDown && (
        <p>
          This website was taken offline by C Note{takenDown.reason ? `: ${takenDown.reason}` : '.'} Visitors
          cannot see it until it is restored.
        </p>
      )}
      {links.length > 0 && (
        <div>
          <p>
            Some links on your published website were flagged as unsafe and are hidden. They show as plain
            text for visitors:
          </p>
          <ul className="list-disc pl-5">
            {links.map((l) => (
              <li key={l} className="break-all">
                {l}
              </li>
            ))}
          </ul>
        </div>
      )}
      {takenDown && abuse?.case_id && (
        <div>
          {sent ? (
            <p>Your appeal was sent. We cannot promise when it will be looked at.</p>
          ) : open ? (
            <form
              className="space-y-2"
              onSubmit={(e) => {
                e.preventDefault()
                if (note.trim()) void appeal()
              }}
            >
              <label className="block">
                Why should this website be restored? You can appeal once.
                <textarea
                  className={`${inputCls} mt-1 w-full text-fg`}
                  rows={3}
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                />
              </label>
              <ErrorText error={err} />
              <div className="flex gap-2">
                <button type="submit" className={btnPrimary} disabled={busy || !note.trim()}>
                  Send appeal
                </button>
                <button type="button" className={btn} onClick={() => setOpen(false)}>
                  Cancel
                </button>
              </div>
            </form>
          ) : (
            <button type="button" className={btn} onClick={() => setOpen(true)}>
              Appeal
            </button>
          )}
        </div>
      )}
    </div>
  )
}
