'use client'
import { useEffect, useId, useRef, useState } from 'react'
import { btnPrimary, inputCls } from '@/components/ui'
import type { PublicForm, PublicFormField } from '@/lib/tables'

type Vals = Record<string, string | boolean | string[]>
type State = 'loading' | 'unavailable' | 'busy' | 'ready' | 'sending' | 'done'

const NO = 'This form is not available.'
const BUSY = 'Too many people are using this form right now. Please try again in a while.'

const isEmpty = (f: PublicFormField, v: Vals[string] | undefined) =>
  f.type === 'checkbox' ? v !== true : Array.isArray(v) ? v.length === 0 : !String(v ?? '').trim()

/** The public form at /tf/<token>: no session, no app chrome. Same-origin API, so no CORS. */
export function PublicTableForm({ token }: { token: string }) {
  const uid = useId()
  const [form, setForm] = useState<PublicForm>()
  const [state, setState] = useState<State>('loading')
  const [vals, setVals] = useState<Vals>({})
  const [errs, setErrs] = useState<Record<string, string>>({})
  const [top, setTop] = useState<string>()
  const summary = useRef<HTMLDivElement>(null)
  const hp = useRef<HTMLInputElement>(null)

  useEffect(() => {
    fetch(`/api/public/table-form/${encodeURIComponent(token)}`, {
      cache: 'no-store',
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
    })
      .then(async (r) => {
        if (r.status === 429) return setState('busy')
        if (!r.ok) return setState('unavailable')
        setForm((await r.json()) as PublicForm)
        setState('ready')
      })
      .catch(() => setState('unavailable'))
  }, [token])

  useEffect(() => {
    if (top || Object.keys(errs).length) summary.current?.focus()
  }, [top, errs])

  const set = (id: string, v: Vals[string]) => setVals((x) => ({ ...x, [id]: v }))
  const fid = (f: PublicFormField) => `${uid}-${f.field_id}`

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!form) return
    const bad: Record<string, string> = {}
    for (const f of form.fields)
      if (f.required && isEmpty(f, vals[f.field_id])) bad[f.field_id] = `${f.label} is required.`
    setTop(undefined)
    setErrs(bad)
    if (Object.keys(bad).length) return
    const values: Record<string, unknown> = {}
    for (const f of form.fields) {
      const v = vals[f.field_id]
      if (f.type === 'checkbox') values[f.field_id] = v === true
      else if (!isEmpty(f, v)) values[f.field_id] = v
    }
    const body = JSON.stringify({ values, hp: hp.current?.value ?? '' })
    setState('sending')
    try {
      const r = await fetch(`/api/public/table-form/${encodeURIComponent(token)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body,
        credentials: 'omit',
        referrerPolicy: 'no-referrer',
      })
      if (r.ok) return setState('done')
      if (r.status === 404) return setState('unavailable')
      const j = await r.json().catch(() => ({}))
      setTop(
        r.status === 429 ? BUSY : (j?.error?.message ?? 'Your response could not be sent. Please try again.'),
      )
    } catch {
      setTop('Your response could not be sent. Check your connection and try again.')
    }
    setState('ready')
  }

  const shell = (children: React.ReactNode) => (
    <main className="mx-auto w-full max-w-[480px] px-4 py-8">
      {children}
      <p className="mt-8 text-center text-[12px] text-muted">Made with C Note</p>
    </main>
  )

  if (state === 'loading')
    return shell(
      <p role="status" className="text-muted">
        Loading.
      </p>,
    )
  if (state === 'unavailable')
    return shell(
      <p role="status" className="text-[15px]">
        {NO}
      </p>,
    )
  if (state === 'busy')
    return shell(
      <p role="status" className="text-[15px]">
        {BUSY}
      </p>,
    )
  if (!form) return null
  if (state === 'done')
    return shell(
      <div role="status" className="space-y-3">
        <h1 className="text-xl font-semibold">Thank you</h1>
        <p className="whitespace-pre-wrap text-[15px]">{form.thank_you || 'Your response has been sent.'}</p>
        <button
          type="button"
          className={`${btnPrimary} min-h-11`}
          onClick={() => {
            setVals({})
            setErrs({})
            setState('ready')
          }}
        >
          Submit another response
        </button>
      </div>,
    )

  const list = Object.entries(errs)
  return shell(
    <form noValidate onSubmit={submit} className="space-y-4" aria-labelledby={`${uid}-title`}>
      <h1 id={`${uid}-title`} className="text-xl font-semibold">
        {form.title}
      </h1>
      {form.intro && <p className="whitespace-pre-wrap text-[15px]">{form.intro}</p>}
      <div ref={summary} tabIndex={-1} role="alert" className="focus:outline-none">
        {(top || list.length > 0) && (
          <div className="rounded-md border border-bad p-2 text-[14px] text-bad">
            {top && <p>{top}</p>}
            {list.length > 0 && (
              <>
                <p>Please fix {list.length === 1 ? 'this' : 'these'} before sending:</p>
                <ul className="list-disc pl-5">
                  {list.map(([id, m]) => (
                    <li key={id}>
                      <a href={`#${uid}-${id}`} className="underline">
                        {m}
                      </a>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </div>
        )}
      </div>

      {form.fields.map((f) => (
        <Question
          key={f.field_id}
          f={f}
          id={fid(f)}
          v={vals[f.field_id]}
          err={errs[f.field_id]}
          set={(v) => set(f.field_id, v)}
        />
      ))}

      {/* Honeypot: off-screen (not display:none), out of the tab order and hidden from assistive technology. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute -left-[9999px] top-auto h-px w-px overflow-hidden"
      >
        <input ref={hp} type="text" name="hp" tabIndex={-1} autoComplete="off" defaultValue="" />
      </div>

      <button
        type="submit"
        disabled={state === 'sending'}
        className={`${btnPrimary} min-h-11 w-full text-[15px]`}
      >
        {state === 'sending' ? 'Sending' : 'Submit'}
      </button>
    </form>,
  )
}

function Question({
  f,
  id,
  v,
  err,
  set,
}: {
  f: PublicFormField
  id: string
  v: Vals[string] | undefined
  err?: string
  set: (v: Vals[string]) => void
}) {
  const dd = [f.help && `${id}-help`, err && `${id}-err`].filter(Boolean).join(' ') || undefined
  const common = {
    id,
    'aria-required': f.required || undefined,
    'aria-invalid': !!err || undefined,
    'aria-describedby': dd,
  }
  const cls = `${inputCls} min-h-11 text-[16px]` // 16px: phones do not zoom on focus
  const mark = f.required ? <span className="text-bad"> (required)</span> : null
  let control: React.ReactNode
  switch (f.type) {
    case 'long_text':
      control = (
        <textarea
          {...common}
          rows={4}
          className={cls}
          value={(v as string) ?? ''}
          onChange={(e) => set(e.target.value)}
        />
      )
      break
    case 'checkbox':
      control = (
        <label className="flex min-h-11 items-center gap-2 text-[15px]">
          <input
            {...common}
            type="checkbox"
            className="h-5 w-5"
            checked={v === true}
            onChange={(e) => set(e.target.checked)}
          />
          <span>
            {f.label}
            {mark}
          </span>
        </label>
      )
      break
    case 'single_select':
      control = (
        <select {...common} className={cls} value={(v as string) ?? ''} onChange={(e) => set(e.target.value)}>
          <option value="">Choose one</option>
          {f.options?.map((o) => (
            <option key={o.id} value={o.id}>
              {o.name}
            </option>
          ))}
        </select>
      )
      break
    case 'multi_select': {
      const cur = (v as string[]) ?? []
      control = (
        <fieldset
          id={id}
          aria-labelledby={`${id}-label`}
          aria-describedby={dd}
          className="m-0 space-y-1 border-0 p-0"
        >
          {f.options?.map((o) => (
            <label key={o.id} className="flex min-h-11 items-center gap-2 text-[15px]">
              <input
                type="checkbox"
                className="h-5 w-5"
                checked={cur.includes(o.id)}
                onChange={(e) => set(e.target.checked ? [...cur, o.id] : cur.filter((x) => x !== o.id))}
              />
              {o.name}
            </label>
          ))}
        </fieldset>
      )
      break
    }
    default: {
      const type =
        (
          {
            number: 'number',
            percent: 'number',
            currency: 'number',
            date: 'date',
            email: 'email',
            url: 'url',
            phone: 'tel',
          } as Record<string, string>
        )[f.type] ?? 'text'
      const input = (
        <input
          {...common}
          type={type}
          step={type === 'number' ? 'any' : undefined}
          inputMode={f.type === 'phone' ? 'tel' : type === 'number' ? 'decimal' : undefined}
          autoComplete={f.type === 'email' ? 'email' : f.type === 'phone' ? 'tel' : 'off'}
          className={f.type === 'currency' ? `${cls} pl-7` : cls}
          value={(v as string) ?? ''}
          onChange={(e) => set(e.target.value)}
        />
      )
      control =
        f.type === 'currency' ? (
          <div className="relative">
            <span
              aria-hidden
              className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted"
            >
              ₹
            </span>
            {input}
          </div>
        ) : (
          input
        )
    }
  }
  return (
    <div className="space-y-1">
      {f.type !== 'checkbox' && (
        <label
          id={`${id}-label`}
          htmlFor={f.type === 'multi_select' ? undefined : id}
          className="block text-[14px] font-medium"
        >
          {f.label}
          {mark}
        </label>
      )}
      {f.help && (
        <p id={`${id}-help`} className="text-[13px] text-muted">
          {f.help}
        </p>
      )}
      {control}
      {err && (
        <p id={`${id}-err`} className="text-[13px] text-bad">
          {err}
        </p>
      )}
    </div>
  )
}
