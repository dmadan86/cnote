'use client'
import { Braces } from 'lucide-react'
import { useState } from 'react'
import { btn, inputCls } from '@/components/ui'
import { useCmd } from '@/lib/hooks'

export const SOURCE_KINDS = [
  ['none', 'No record'],
  ['contact', 'Contact'],
  ['deal', 'Deal'],
  ['invoice', 'Invoice'],
  ['item', 'Item'],
] as const

type FieldRow = { path: string; label: string }

/** Insert-field picker: a button that opens a record-kind + field chooser; calls onInsert with the `{{path}}` text. */
export function InsertField({ ws, onInsert }: { ws: string; onInsert: (text: string) => void }) {
  const [open, setOpen] = useState(false)
  const [kind, setKind] = useState('deal')
  const fields = useCmd<FieldRow[]>(ws, 'template.fields', { source_kind: kind }, open)
  return (
    <div className="relative">
      <button
        type="button"
        className={btn}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}
      >
        <Braces size={14} /> Field
      </button>
      {open && (
        <div
          role="dialog"
          aria-label="Insert field"
          className="absolute right-0 z-20 mt-1 flex w-64 flex-col gap-2 rounded-md border border-line bg-canvas p-2 shadow-lg"
        >
          <select
            aria-label="Field source"
            className={inputCls}
            value={kind}
            onChange={(e) => setKind(e.target.value)}
          >
            {SOURCE_KINDS.map(([k, label]) => (
              <option key={k} value={k}>
                {label}
              </option>
            ))}
          </select>
          <select
            aria-label="Field"
            className={inputCls}
            value=""
            onChange={(e) => {
              if (!e.target.value) return
              onInsert(`{{${e.target.value}}}`)
              setOpen(false)
            }}
          >
            <option value="">Choose a field...</option>
            {fields.data?.map((f) => (
              <option key={f.path} value={f.path}>
                {f.label}
              </option>
            ))}
          </select>
        </div>
      )}
    </div>
  )
}
