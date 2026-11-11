'use client'
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { btn } from './ui'

export function Secret({ value, onDone }: { value: string; onDone: () => void }) {
  const [copied, setCopied] = useState(false)
  return (
    <div role="status" className="space-y-2 rounded-md border border-line bg-warn-bg p-3">
      <p className="text-[13px] font-medium">Copy this key now. It will not be shown again.</p>
      <div className="flex items-center gap-2">
        <code
          data-testid="secret"
          className="min-w-0 flex-1 break-all rounded bg-canvas px-2 py-1 text-[13px]"
        >
          {value}
        </code>
        <button
          type="button"
          className={btn}
          onClick={async () => {
            await navigator.clipboard.writeText(value).catch(() => {})
            setCopied(true)
          }}
        >
          {copied ? <Check size={14} /> : <Copy size={14} />} {copied ? 'Copied' : 'Copy'}
        </button>
        <button type="button" className={btn} onClick={onDone}>
          Done
        </button>
      </div>
    </div>
  )
}
