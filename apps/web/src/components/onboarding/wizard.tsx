'use client'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useEffect, useRef, useState } from 'react'
import { STATES } from '@/components/billing/fmt'
import { BusinessTypePicker } from '@/components/business-type'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, Field, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useFinance } from '@/lib/hooks'
import { moduleOn } from '@/lib/modules'
import { GOALS, GSTIN, type Pending, runPending, writePending } from '@/lib/onboarding'

const STEPS = 3
type Profile = { legal_name?: string; state_code?: string; gstin?: string; address?: string }

const gstinError = (g: string) =>
  !g
    ? undefined
    : g.length !== 15
      ? 'This GSTIN is not 15 characters.'
      : GSTIN.test(g)
        ? undefined
        : 'This GSTIN does not look right. Check it against your GST certificate.'

/** First-run setup: three steps, every one skippable. It never blocks the app: "Skip for now" and "Go to C Note" always reach Home. */
export function Welcome() {
  const { ws, me, reload, modules } = useShell()
  const router = useRouter()
  const home = `/w/${ws}`
  const role = me.workspaces.find((w) => w.id === ws)?.role
  const allowed = role === 'owner' || role === 'admin'
  const finance = useFinance(ws)
  // only what this person can actually do: not in an uninstalled module, and money flows need finance access
  const goals = GOALS.filter((g) => moduleOn(modules, g.module ?? '') && (!g.finance || finance))
  const [step, setStep] = useState(1)
  const [type, setType] = useState<string>()
  const [name, setName] = useState('')
  const [state, setState] = useState('')
  const [gstin, setGstin] = useState('')
  const [address, setAddress] = useState('')
  const [goal, setGoal] = useState<string>()
  const [gstinTouched, setGstinTouched] = useState(false)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  // saves that failed: they ride along to Home, which offers "Try again"
  const failed = useRef<Pending>({})
  // Every save run gets its own controller. A newer run, leaving, or navigating away aborts the one in flight, so a
  // late answer never changes what is shown or what is kept for "Try again".
  const run = useRef<AbortController | undefined>(undefined)
  const next = () => {
    run.current?.abort()
    const c = new AbortController()
    run.current = c
    return c
  }
  const heading = useRef<HTMLHeadingElement>(null)

  // navigating away aborts the run in flight; whatever did not finish is kept for Home's "Try again"
  useEffect(
    () => () => {
      run.current?.abort()
      if (Object.keys(failed.current).length) writePending(ws, failed.current)
    },
    [ws],
  )

  useEffect(() => {
    if (me && !allowed) router.replace(home)
  }, [me, allowed, router, home])

  // Prefill from the real records. A failed read leaves the fields empty; it never shows an error page.
  useEffect(() => {
    if (!allowed) return
    let live = true
    cmd<Profile & { name?: string }>(ws, 'company.get').then(
      (p) => {
        if (!live) return
        setName((n) => n || p.legal_name || p.name || '')
        setState((s) => s || p.state_code || '')
        setGstin((g) => g || p.gstin || '')
        setAddress((a) => a || p.address || '')
      },
      () => {},
    )
    cmd<{ settings: { business_type?: string } }>(ws, 'workspace.get').then(
      (w) => live && setType((t) => t ?? w.settings.business_type),
      () => {},
    )
    return () => {
      live = false
    }
  }, [ws, allowed])

  // biome-ignore lint/correctness/useExhaustiveDependencies: move focus to the heading on every step change
  useEffect(() => heading.current?.focus(), [step])

  const gstinErr = gstinError(gstin)
  const gstinState = gstin.length === 15 && !gstinErr ? gstin.slice(0, 2) : undefined
  const stateCode = gstinState ?? state

  /** Always reaches Home. Anything that does not save is kept for the "Try again" notice there. */
  async function leave(to = home, extra: Pending = {}) {
    setBusy(true)
    const c = next()
    Object.assign(failed.current, extra, { complete: true })
    // a hung request must not hold the person here: after a few seconds it is aborted, kept for "Try again", and we go
    const timer = setTimeout(() => c.abort(), 4000)
    await runPending(ws, { ...failed.current }, c.signal, (k) => delete failed.current[k])
    clearTimeout(timer)
    writePending(ws, failed.current)
    reload()
    router.push(to)
  }

  /** Saves what this step collected; on failure stays here with the reason so the person can retry or skip. */
  async function save(p: Pending, onOk: () => void) {
    setBusy(true)
    setError(undefined)
    const c = next()
    Object.assign(failed.current, p) // newest values win; each is cleared only when it is saved
    const { error: e } = await runPending(ws, p, c.signal, (k) => delete failed.current[k])
    if (c.signal.aborted) return // superseded by a newer save or by leaving
    setBusy(false)
    if (e) return setError(e.message)
    onOk()
  }

  function submit(ev: React.FormEvent) {
    ev.preventDefault()
    if (busy) return
    if (step === 1) {
      if (!type) return setError('Pick the kind of business you run.')
      const p: Pending = { type: { id: type } }
      if (name.trim()) p.legal_name = name.trim()
      return save(p, () => setStep(2))
    }
    if (step === 2) {
      setGstinTouched(true)
      if (gstinErr) return
      if (!stateCode) return setError('Choose your state.')
      const company: Record<string, string> = { state_code: stateCode }
      if (gstin) company.gstin = gstin
      if (address.trim()) company.address = address.trim()
      return save({ company }, () => setStep(3))
    }
    const g = goals.find((x) => x.id === goal)
    if (!g) return setError('Pick one to start with.')
    return leave(`${home}${g.path}`, { goal: g.id })
  }

  if (!allowed) return null
  const titles = ['What does your business do?', 'Your business details', 'What do you want to do first?']
  const err = (id: string) => (id === 'gstin' && gstinTouched && gstinErr ? gstinErr : undefined)
  return (
    <form
      onSubmit={submit}
      noValidate
      className="mx-auto flex min-h-full max-w-2xl flex-col px-4 pt-6 sm:px-6"
    >
      <div className="mb-4 flex items-center justify-between gap-3">
        <p id="onboarding-step" className="text-[13px] text-muted">
          Step {step} of {STEPS}
          <span aria-hidden="true" className="ml-3 inline-flex gap-1 align-middle">
            {[1, 2, 3].map((i) => (
              <span key={i} className={`h-1 w-8 rounded-full ${i <= step ? 'bg-accent' : 'bg-line'}`} />
            ))}
          </span>
        </p>
        <Link href={home} onClick={(e) => (e.preventDefault(), leave())} className={`${btn} min-h-11`}>
          Go to C Note
        </Link>
      </div>
      <h1
        ref={heading}
        tabIndex={-1}
        aria-describedby="onboarding-step"
        className="text-xl font-semibold focus:outline-none"
      >
        {titles[step - 1]}
      </h1>

      <div className="mt-4 flex-1 space-y-4 pb-6">
        {step === 1 && (
          <>
            <p className="text-[13px] text-muted">
              This sets your deal stage names. You can change it later.
            </p>
            <BusinessTypePicker current={type} onPick={setType} withOther disabled={busy} />
            <Field label="Business name">
              {(id) => (
                <input
                  id={id}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  autoComplete="organization"
                  className={`${inputCls} min-h-11`}
                />
              )}
            </Field>
          </>
        )}
        {step === 2 && (
          <>
            <p className="text-[13px] text-muted">These print on your quotations and invoices.</p>
            <Field label="State">
              {(id) => (
                <>
                  <select
                    id={id}
                    value={stateCode}
                    disabled={!!gstinState}
                    onChange={(e) => setState(e.target.value)}
                    aria-describedby={gstinState ? `${id}-note` : undefined}
                    className={`${inputCls} min-h-11`}
                  >
                    <option value="">Choose your state</option>
                    {STATES.map(([c, n]) => (
                      <option key={c} value={c}>
                        {n}
                      </option>
                    ))}
                  </select>
                  {gstinState && (
                    <p id={`${id}-note`} className="text-[12px] text-muted">
                      Set from your GSTIN
                    </p>
                  )}
                </>
              )}
            </Field>
            <Field label="GSTIN (optional)">
              {(id) => (
                <>
                  <input
                    id={id}
                    value={gstin}
                    maxLength={15}
                    autoCapitalize="characters"
                    autoCorrect="off"
                    spellCheck={false}
                    onChange={(e) => setGstin(e.target.value.toUpperCase().trim())}
                    onBlur={() => setGstinTouched(true)}
                    aria-invalid={!!err('gstin') || undefined}
                    aria-describedby={`${id}-hint`}
                    className={`${inputCls} min-h-11 font-mono ${err('gstin') ? 'border-bad' : ''}`}
                  />
                  <p id={`${id}-hint`} className="text-[12px] text-muted">
                    15 characters, as on your GST certificate. Leave empty if you are not registered for GST.
                  </p>
                  {err('gstin') && (
                    <p role="alert" className="text-[13px] text-bad">
                      {err('gstin')}
                    </p>
                  )}
                </>
              )}
            </Field>
            <Field label="Address (optional)">
              {(id) => (
                <textarea
                  id={id}
                  rows={2}
                  value={address}
                  onChange={(e) => setAddress(e.target.value)}
                  autoComplete="street-address"
                  className={inputCls}
                />
              )}
            </Field>
          </>
        )}
        {step === 3 && (
          <fieldset className="space-y-2">
            <legend className="mb-2 text-[13px] text-muted">
              Pick one. We will open it for you. Everything else stays in the sidebar.
            </legend>
            {goals.map((g) => (
              <label
                key={g.id}
                className="flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border border-line p-3 hover:bg-hover has-[:checked]:border-accent has-[:checked]:bg-hover has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-accent"
              >
                <input
                  type="radio"
                  name="goal"
                  value={g.id}
                  checked={goal === g.id}
                  onChange={() => setGoal(g.id)}
                  className="size-4"
                />
                <span className="text-[14px] font-medium">{g.label}</span>
              </label>
            ))}
          </fieldset>
        )}
        {error && (
          <p role="alert" className="text-[13px] text-bad">
            {error}
          </p>
        )}
      </div>

      <div className="sticky bottom-0 -mx-4 flex flex-wrap items-center justify-end gap-2 border-t border-line bg-canvas px-4 py-3 sm:-mx-6 sm:px-6">
        {step > 1 && (
          <button
            type="button"
            className={`${btn} mr-auto min-h-11`}
            disabled={busy}
            onClick={() => {
              setError(undefined)
              setStep(step - 1)
            }}
          >
            Back
          </button>
        )}
        <button type="button" className={`${btn} min-h-11`} onClick={() => leave()}>
          Skip for now
        </button>
        <button type="submit" className={`${btnPrimary} min-h-11`} disabled={busy}>
          {step === STEPS ? 'Finish' : 'Continue'}
        </button>
      </div>
    </form>
  )
}
