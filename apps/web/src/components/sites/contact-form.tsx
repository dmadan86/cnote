'use client'
import { Check } from 'lucide-react'
import { useId, useState } from 'react'
import { siteBtn, siteField } from './blocks'

/**
 * The live contact form on a published page (design 7, 17.6). Posts same-origin to the API; no CORS, no cookie.
 * Consent is an unticked, required checkbox; `company_website` is the honeypot people never fill.
 */
export function ContactFormLive({
  siteId,
  business,
  phone,
  whatsapp,
  submitLabel,
  paused,
}: {
  siteId: string
  business: string
  phone?: string
  whatsapp?: string
  submitLabel?: string
  paused?: boolean
}) {
  const id = useId()
  const [state, setState] = useState<'idle' | 'sending' | 'done'>('idle')
  const [err, setErr] = useState('')
  const [fieldErr, setFieldErr] = useState<Record<string, string>>({})
  const [sentName, setSentName] = useState('')
  const who = business || 'this business'
  const consentText = `I agree that ${who} may use these details to reply to my enquiry.`
  const callUs = phone ? `, or call ${phone}` : ''

  if (paused)
    return (
      <p role="status" className="text-[15px]">
        This form is paused right now. {phone ? `Please call ${phone}.` : 'Please check back soon.'}
      </p>
    )
  if (state === 'done')
    return (
      <div role="status" className="space-y-3">
        <p className="flex items-center gap-2 font-semibold">
          <Check size={18} aria-hidden="true" className="text-[var(--site-primary)]" />
          Thanks{sentName ? `, ${sentName}` : ''}. We've got your message.
        </p>
        <p className="text-[15px] text-[var(--site-muted)]">{who} usually replies within a day.</p>
        {whatsapp && (
          <a href={whatsapp} rel="noopener noreferrer" className={`${siteBtn} w-full`}>
            Chat on WhatsApp
          </a>
        )}
      </div>
    )

  const submit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    const v = (k: string) => String(f.get(k) ?? '').trim()
    const fe: Record<string, string> = {}
    if (!v('name')) fe.name = 'Enter your name'
    if (v('phone').replace(/\D/g, '').length < 10) fe.phone = 'Enter a 10-digit mobile number'
    if (v('email') && !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v('email'))) fe.email = 'Enter a valid email'
    if (f.get('consent') !== 'on') fe.consent = 'Tick the box to send your enquiry'
    setFieldErr(fe)
    if (Object.keys(fe).length) return
    setState('sending')
    setErr('')
    try {
      const r = await fetch(`/api/sites/form/${siteId}`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        credentials: 'omit',
        body: JSON.stringify({
          name: v('name'),
          phone: v('phone'),
          email: v('email'),
          message: v('message'),
          consent: f.get('consent') === 'on',
          consent_text: consentText,
          company_website: v('company_website'),
        }),
      })
      if (!r.ok) {
        const j = (await r.json().catch(() => ({}))) as { error?: { message?: string } }
        setErr(
          r.status === 429
            ? `Too many messages right now. Please try again later${callUs}.`
            : (j.error?.message ?? `Couldn't send. Please try again${callUs}.`),
        )
        setState('idle') // the typed values stay in the fields
        return
      }
      setSentName(v('name'))
      setState('done')
    } catch {
      setErr(`Couldn't send. Please try again${callUs}.`)
      setState('idle')
    }
  }

  const row = (name: string, label: string, children: React.ReactNode) => (
    <div key={name}>
      <label htmlFor={`${id}-${name}`} className="mb-1 block text-sm font-medium">
        {label}
      </label>
      {children}
      {fieldErr[name] && (
        <p id={`${id}-${name}-err`} className="mt-1 text-[13px] text-[#b91c1c]">
          {fieldErr[name]}
        </p>
      )}
    </div>
  )
  const inp = (name: string, extra: React.InputHTMLAttributes<HTMLInputElement> = {}) => (
    <input
      id={`${id}-${name}`}
      name={name}
      aria-invalid={!!fieldErr[name]}
      aria-describedby={fieldErr[name] ? `${id}-${name}-err` : undefined}
      className={siteField}
      {...extra}
    />
  )

  return (
    <form className="space-y-3" onSubmit={submit} noValidate>
      {row('name', 'Your name', inp('name', { required: true, maxLength: 100, autoComplete: 'name' }))}
      {row(
        'phone',
        'Mobile number',
        inp('phone', { required: true, type: 'tel', inputMode: 'tel', maxLength: 20, autoComplete: 'tel' }),
      )}
      {row(
        'email',
        'Email (optional)',
        inp('email', { type: 'email', maxLength: 120, autoComplete: 'email' }),
      )}
      {row(
        'message',
        'Message (optional)',
        <textarea id={`${id}-message`} name="message" rows={3} maxLength={1000} className={siteField} />,
      )}
      {/* honeypot: off-screen, out of tab order and hidden from screen readers */}
      <div aria-hidden="true" style={{ position: 'absolute', left: -9999 }}>
        <input name="company_website" tabIndex={-1} autoComplete="off" defaultValue="" />
      </div>
      <label className="flex items-start gap-2 text-[14px]">
        <input type="checkbox" name="consent" required className="mt-1 size-4 accent-[var(--site-primary)]" />
        <span>{consentText}</span>
      </label>
      {fieldErr.consent && <p className="text-[13px] text-[#b91c1c]">{fieldErr.consent}</p>}
      <p className="text-[13px] text-[var(--site-muted)]">
        Your details go to {who} and are stored by C Note, which hosts this site. Ask {who} to correct or
        delete them at any time.
      </p>
      {err && (
        <p
          role="alert"
          className="rounded-[var(--site-radius)] bg-[#fee2e2] px-3 py-2 text-[14px] text-[#991b1b]"
        >
          {err}
        </p>
      )}
      <button
        type="submit"
        disabled={state === 'sending'}
        className={`${siteBtn} w-full disabled:opacity-60`}
      >
        {state === 'sending' ? 'Sending…' : submitLabel || 'Send enquiry'}
      </button>
    </form>
  )
}
