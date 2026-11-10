'use client'
import { Search } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btnPrimary, ErrorText, FreshnessPill, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'

type Answer = {
  answered: boolean
  answer: string
  citations: {
    note_id: string
    title: string
    freshness: string
    verified_at: string | null
    snippet: string
  }[]
  routed_to?: string | null
}

export function AskBox() {
  const { ws, members } = useShell()
  const [res, setRes] = useState<Answer>()
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const routed = members.find((m) => m.user_id === res?.routed_to)

  return (
    <section aria-label="Ask the docs" className="space-y-3">
      <form
        className="flex gap-2"
        onSubmit={async (e) => {
          e.preventDefault()
          setBusy(true)
          try {
            setRes(await cmd<Answer>(ws, 'doc.ask', { question: new FormData(e.currentTarget).get('q') }))
            setErr(undefined)
          } catch (x) {
            setErr((x as Error).message)
          } finally {
            setBusy(false)
          }
        }}
      >
        <input
          name="q"
          required
          aria-label="Ask a question"
          placeholder="Ask a question about your docs..."
          className={`${inputCls} flex-1 py-2.5`}
        />
        <button type="submit" disabled={busy} className={btnPrimary}>
          <Search size={14} /> {busy ? 'Asking...' : 'Ask'}
        </button>
      </form>
      <ErrorText error={err} />
      {res && (
        <div role="status" className="space-y-3 rounded-lg border border-line p-4">
          <p className="whitespace-pre-wrap">{res.answer}</p>
          {res.answered ? (
            <ul aria-label="Sources" className="grid gap-2 sm:grid-cols-2">
              {res.citations.map((c) => (
                <li key={c.note_id}>
                  <Link
                    href={`/w/${ws}/n/${c.note_id}`}
                    className="block rounded-md border border-line p-3 hover:bg-hover"
                  >
                    <span className="block font-medium">{c.title || 'Untitled'}</span>
                    <span className="mt-1 flex flex-wrap items-center gap-2 text-[12px] text-muted">
                      <FreshnessPill freshness={c.freshness} verifyBy={null} />
                      {c.verified_at ? `Verified ${c.verified_at.slice(0, 10)}` : 'Never verified'}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-[13px] text-muted">
              Capture request sent to <strong>{routed?.name ?? 'a workspace admin'}</strong>.
            </p>
          )}
        </div>
      )}
    </section>
  )
}
