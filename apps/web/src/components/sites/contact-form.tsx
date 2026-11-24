'use client'
import { Check } from 'lucide-react'
import { useEffect, useId, useState } from 'react'
import { siteBtn, siteField } from './blocks'
import { t } from './strings'

/** The approved consent wording the server stores with a submission; the server keeps the text, never this client. */
const CONSENT_ID = 'enquiry-v1'

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
  items,
  lang = 'en',
}: {
  siteId: string
  business: string
  phone?: string
  whatsapp?: string
  submitLabel?: string
  paused?: boolean
  /** Catalogue item id -> name, from the server; the form only shows the name for an id it knows (design 2). */
  items?: Record<string, string>
  /** The page's language: the visible text and the consent sentence are in it; the server stores its own text for it. */
  lang?: string
}) {
  const id = useId()
  // `?item=` is read only on the client; an id that is not in the server's map is ignored.
  const [item, setItem] = useState('')
  useEffect(() => {
    const v = new URLSearchParams(location.search).get('item')?.toLowerCase() ?? ''
    if (v && items && Object.hasOwn(items, v)) setItem(v)
  }, [items])
  const [state, setState] = useState<'idle' | 'sending' | 'done'>('idle')
  const [err, setErr] = useState('')
  const [fieldErr, setFieldErr] = useState<Record<string, string>>({})
  const [sentName, setSentName] = useState('')
  const who = business || t(lang, 'form_business_fallback')
  const consentText = t(lang, 'form_consent', { business: who })
  const callUs = phone ? t(lang, 'form_call_suffix', { phone }) : ''

  if (paused)
    return (
      <p role="status" className="text-[15px]">
        {phone ? t(lang, 'form_paused_call', { phone }) : t(lang, 'form_paused')}
      </p>
    )
  if (state === 'done')
    return (
      <div role="status" className="space-y-3">
        <p className="flex items-center gap-2 font-semibold">
          <Check size={18} aria-hidden="true" className="text-[var(--site-primary)]" />
          {sentName ? t(lang, 'form_thanks_name', { name: sentName }) : t(lang, 'form_thanks')}
        </p>
        <p className="text-[15px] text-[var(--site-muted)]">
          {t(lang, 'form_reply_soon', { business: who })}
        </p>
        {whatsapp && (
          <a href={whatsapp} rel="noopener noreferrer" className={`${siteBtn} w-full`}>
            {t(lang, 'chat_on_whatsapp')}
          </a>
        )}
      </div>
    )

  const submit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    const v = (k: string) => String(f.get(k) ?? '').trim()
    const fe: Record<string, string> = {}
    if (!v('name')) fe.name = t(lang, 'form_err_name')
    if (v('phone').replace(/\D/g, '').length < 10) fe.phone = t(lang, 'form_err_phone')
    if (v('email') && !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v('email'))) fe.email = t(lang, 'form_err_email')
    if (f.get('consent') !== 'on') fe.consent = t(lang, 'form_err_consent')
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
          consent_id: CONSENT_ID,
          lang,
          ...(item ? { item_id: item } : {}),
          company_website: v('company_website'),
        }),
      })
      if (!r.ok) {
        const j = (await r.json().catch(() => ({}))) as { error?: { message?: string } }
        setErr(
          r.status === 429
            ? t(lang, 'form_err_rate', { call: callUs })
            : (j.error?.message ?? t(lang, 'form_err_send', { call: callUs })),
        )
        setState('idle') // the typed values stay in the fields
        return
      }
      setSentName(v('name'))
      setState('done')
    } catch {
      setErr(t(lang, 'form_err_send', { call: callUs }))
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
      {item && items && (
        <p className="rounded-[var(--site-radius)] bg-[var(--site-tint)] px-3 py-2 text-[14px]">
          {t(lang, 'form_about')} <strong>{items[item]}</strong>
        </p>
      )}
      {row(
        'name',
        t(lang, 'form_label_name'),
        inp('name', { required: true, maxLength: 100, autoComplete: 'name' }),
      )}
      {row(
        'phone',
        t(lang, 'form_label_phone'),
        inp('phone', { required: true, type: 'tel', inputMode: 'tel', maxLength: 20, autoComplete: 'tel' }),
      )}
      {row(
        'email',
        t(lang, 'form_label_email'),
        inp('email', { type: 'email', maxLength: 120, autoComplete: 'email' }),
      )}
      {row(
        'message',
        t(lang, 'form_label_message'),
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
      <p className="text-[13px] text-[var(--site-muted)]">{t(lang, 'form_privacy', { business: who })}</p>
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
        {state === 'sending' ? t(lang, 'form_sending') : submitLabel || t(lang, 'form_submit')}
      </button>
    </form>
  )
}
