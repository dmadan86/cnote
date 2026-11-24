'use client'
import { Mail, MessageCircle } from 'lucide-react'
import { waHref } from '@/lib/sites'

export type Support = { whatsapp?: string; email?: string }
export const hasSupport = (s: Support) => !!(waHref(s.whatsapp) || s.email)

/** Support is open Monday to Saturday, 10 am to 7 pm India time. */
function openNow(now = new Date()) {
  const p = new Intl.DateTimeFormat('en-GB', {
    timeZone: 'Asia/Kolkata',
    weekday: 'short',
    hour: 'numeric',
    hourCycle: 'h23',
  }).formatToParts(now)
  const day = p.find((x) => x.type === 'weekday')?.value
  const hour = Number(p.find((x) => x.type === 'hour')?.value)
  return day !== 'Sun' && hour >= 10 && hour < 19
}

/** "Talk to a person": hours and two links built from the server's SUPPORT_WHATSAPP and SUPPORT_EMAIL. No backend. */
export function HelpCard({ support, site }: { support: Support; site: string }) {
  const wa = waHref(support.whatsapp, `Hi, I need help with my website (${site})`)
  const open = openNow()
  return (
    <section
      aria-label="Talk to a person"
      className="space-y-2 rounded-lg border border-line p-3 text-[13px]"
    >
      <h3 className="font-semibold">Talk to a person</h3>
      <p className="flex items-center gap-1.5">
        <span aria-hidden="true" className={`size-2 rounded-full ${open ? 'bg-ok' : 'bg-muted'}`} />
        {open ? 'Open now' : 'Closed now'} · Mon–Sat, 10 am – 7 pm IST
      </p>
      <div className="flex flex-wrap gap-2">
        {wa && (
          <a
            href={wa}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1.5 rounded-md border border-line px-3 py-1.5 font-medium hover:bg-hover"
          >
            <MessageCircle size={14} aria-hidden="true" /> Chat on WhatsApp
          </a>
        )}
        {support.email && (
          <a
            href={`mailto:${support.email}?subject=${encodeURIComponent(`Website help: ${site}`)}`}
            className="inline-flex items-center gap-1.5 rounded-md border border-line px-3 py-1.5 font-medium hover:bg-hover"
          >
            <Mail size={14} aria-hidden="true" /> Email us
          </a>
        )}
      </div>
    </section>
  )
}
