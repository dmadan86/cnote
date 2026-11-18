'use client'
import { useEffect, useState } from 'react'
import { ErrorText } from '@/components/ui'
import { cmd } from '@/lib/api'

export type Preview = { subject: string; text: string; html: string; sample?: boolean; record_label?: string }

/** The rendered email in a locked-down iframe: no scripts, no same-origin, fixed height. */
export function EmailFrame({ html, height = 460 }: { html: string; height?: number }) {
  return (
    <iframe
      sandbox=""
      srcDoc={html}
      title="Email preview"
      style={{ height }}
      className="w-full rounded-md border border-line bg-canvas"
    />
  )
}

/** Debounced (400 ms) server preview for unsaved wording. Returns the last good render plus loading/error state. */
export function useLivePreview(ws: string, event: string, subject: string, body: string) {
  const [data, setData] = useState<Preview>()
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(true)
  useEffect(() => {
    let live = true
    setBusy(true)
    const t = setTimeout(() => {
      cmd<Preview>(ws, 'email_template.preview', { event, subject, body })
        .then((d) => {
          if (!live) return
          setData(d)
          setError(undefined)
        })
        .catch((e) => live && setError((e as Error).message))
        .finally(() => live && setBusy(false))
    }, 400)
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [ws, event, subject, body])
  return { data, error, busy }
}

export function PreviewPane({
  ws,
  event,
  subject,
  body,
  fromName,
}: {
  ws: string
  event: string
  subject: string
  body: string
  fromName: string
}) {
  const { data, error, busy } = useLivePreview(ws, event, subject, body)
  return (
    <aside aria-label="Preview" className="space-y-2 lg:sticky lg:top-6 lg:self-start">
      <p className="text-[12px] font-medium text-muted" aria-live="polite">
        Preview with: {data ? (data.sample ? 'Sample' : data.record_label) : '...'}
        {busy && data ? ' (updating)' : ''}
      </p>
      <ErrorText error={error ? `Preview unavailable: ${error}` : undefined} />
      {data ? (
        <>
          <dl className="space-y-0.5 text-[12px]">
            <div className="flex gap-2">
              <dt className="w-14 shrink-0 text-muted">From</dt>
              <dd className="min-w-0 truncate">{fromName || 'Your business'} via C Note</dd>
            </div>
            <div className="flex gap-2">
              <dt className="w-14 shrink-0 text-muted">Subject</dt>
              <dd className="min-w-0 font-medium">{data.subject}</dd>
            </div>
          </dl>
          <EmailFrame html={data.html} />
          <p className="text-[12px] text-muted">The link in a real email is created when it is sent.</p>
        </>
      ) : (
        !error && <div aria-busy="true" className="h-64 animate-pulse rounded-lg bg-hover" />
      )}
    </aside>
  )
}
