'use client'
import { useState } from 'react'
import { siteApi3b } from '@/lib/sites'

const REASONS = [
  ['scam', 'Scam or fraud'],
  ['phishing', 'Phishing or fake login'],
  ['illegal', 'Illegal goods or services'],
  ['impersonation', 'Pretends to be someone else'],
  ['other', 'Something else'],
] as const

/** "Report this site" on a published page. Without JavaScript the link still goes to the support route. */
export function ReportDialog({ siteId, url, href }: { siteId: string; url: string; href: string }) {
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [details, setDetails] = useState('')
  const [trap, setTrap] = useState('')
  const [state, setState] = useState<'idle' | 'sending' | 'done' | 'error'>('idle')
  const send = async () => {
    setState('sending')
    try {
      await siteApi3b.abuseReport(siteId, {
        reason,
        details: details.trim() || undefined,
        url,
        company_website: trap,
      })
      setState('done')
    } catch {
      setState('error')
    }
  }
  return (
    <>
      <a
        href={href}
        rel="noopener noreferrer"
        className="underline-offset-4 hover:underline"
        onClick={(e) => {
          e.preventDefault()
          setOpen(true)
        }}
      >
        Report this site
      </a>
      {open && (
        <dialog
          ref={(d) => {
            if (d && !d.open) d.showModal()
          }}
          aria-label="Report this site"
          onClose={() => setOpen(false)}
          className="m-auto w-full max-w-md rounded-lg border border-[var(--site-border)] bg-white p-4 text-left text-[14px] text-neutral-900 shadow-xl"
        >
          <h2 className="mb-2 text-[16px] font-semibold">Report this site</h2>
          {state === 'done' ? (
            <>
              <p role="status">
                Thank you. Your report has been recorded and will be looked at. We cannot say when, and we may
                not reply.
              </p>
              <button
                type="button"
                className="mt-4 rounded-md border px-3 py-1.5"
                onClick={() => setOpen(false)}
              >
                Close
              </button>
            </>
          ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault()
                if (reason && state !== 'sending') void send()
              }}
            >
              <fieldset className="space-y-1">
                <legend className="mb-1 font-medium">What is wrong?</legend>
                {REASONS.map(([v, l]) => (
                  <label key={v} className="flex items-center gap-2">
                    <input
                      type="radio"
                      name="reason"
                      value={v}
                      checked={reason === v}
                      onChange={() => setReason(v)}
                    />
                    {l}
                  </label>
                ))}
              </fieldset>
              <label className="mt-3 block">
                <span className="font-medium">Details (optional)</span>
                <textarea
                  value={details}
                  onChange={(e) => setDetails(e.target.value)}
                  maxLength={1000}
                  rows={3}
                  className="mt-1 w-full rounded-md border p-2"
                />
              </label>
              {/* Honeypot: people never see it; bots fill it. */}
              <input
                type="text"
                name="company_website"
                value={trap}
                onChange={(e) => setTrap(e.target.value)}
                tabIndex={-1}
                autoComplete="off"
                aria-hidden="true"
                className="absolute -left-[9999px] h-0 w-0 opacity-0"
              />
              {state === 'error' && (
                <p role="alert" className="mt-2 text-red-700">
                  Could not send the report. Please try again later.
                </p>
              )}
              <div className="mt-4 flex gap-2">
                <button
                  type="submit"
                  disabled={!reason || state === 'sending'}
                  className="rounded-md bg-neutral-900 px-3 py-1.5 text-white disabled:opacity-50"
                >
                  {state === 'sending' ? 'Sending…' : 'Send report'}
                </button>
                <button
                  type="button"
                  className="rounded-md border px-3 py-1.5"
                  onClick={() => setOpen(false)}
                >
                  Cancel
                </button>
              </div>
            </form>
          )}
        </dialog>
      )}
    </>
  )
}
