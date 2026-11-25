'use client'
import { useEffect, useRef, useState } from 'react'
import { useShell } from '@/components/shell'
import { type Pending, readPending, runPending, writePending } from '@/lib/onboarding'

/** Shown on Home when a setup save did not go through. Setup never blocks, so this is where the person finds out. */
export function SetupToast() {
  const { ws } = useShell()
  const [left, setLeft] = useState<Pending>({})
  const [busy, setBusy] = useState(false)
  const run = useRef<AbortController | undefined>(undefined)
  useEffect(() => setLeft(readPending(ws)), [ws])
  useEffect(() => () => run.current?.abort(), [])
  if (!Object.keys(left).length) return null
  const retry = async () => {
    setBusy(true)
    run.current?.abort()
    const c = new AbortController()
    run.current = c
    // the latest stored values, not the ones this notice was first shown with
    const r = await runPending(ws, readPending(ws), c.signal)
    if (c.signal.aborted) return // a newer retry or leaving Home: its answer is the one that counts
    writePending(ws, r.left)
    setLeft(r.left)
    setBusy(false)
  }
  const dismiss = () => {
    run.current?.abort()
    writePending(ws, {})
    setLeft({})
  }
  return (
    <div
      role="status"
      className="fixed right-4 bottom-4 z-50 flex max-w-sm flex-wrap items-center gap-3 rounded-lg border border-line bg-canvas p-3 text-[13px] shadow-lg"
    >
      <span>Some of your setup was not saved.</span>
      <button type="button" disabled={busy} onClick={retry} className="font-medium underline">
        Try again
      </button>
      <button type="button" onClick={dismiss} className="text-muted underline">
        Dismiss
      </button>
    </div>
  )
}
