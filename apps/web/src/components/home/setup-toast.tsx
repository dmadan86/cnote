'use client'
import { useEffect, useState } from 'react'
import { useShell } from '@/components/shell'
import { type Pending, readPending, runPending, writePending } from '@/lib/onboarding'

/** Shown on Home when a setup save did not go through. Setup never blocks, so this is where the person finds out. */
export function SetupToast() {
  const { ws } = useShell()
  const [left, setLeft] = useState<Pending>({})
  const [busy, setBusy] = useState(false)
  useEffect(() => setLeft(readPending(ws)), [ws])
  if (!Object.keys(left).length) return null
  const retry = async () => {
    setBusy(true)
    const r = await runPending(ws, left)
    writePending(ws, r.left)
    setLeft(r.left)
    setBusy(false)
  }
  const dismiss = () => {
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
