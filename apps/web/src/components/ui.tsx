'use client'
import { type LucideIcon, X } from 'lucide-react'
import { useEffect, useId, useRef } from 'react'

export const btn =
  'inline-flex items-center justify-center gap-1.5 rounded-md border border-line px-3 py-1.5 text-[13px] font-medium hover:bg-hover disabled:opacity-50'
export const btnPrimary =
  'inline-flex items-center justify-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-accent-fg hover:opacity-90 disabled:opacity-50'
export const inputCls =
  'w-full rounded-md border border-line bg-canvas px-2.5 py-1.5 text-[14px] placeholder:text-muted'

export function Field({ label, children }: { label: string; children: (id: string) => React.ReactNode }) {
  const id = useId()
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block text-[12px] font-medium text-muted">
        {label}
      </label>
      {children(id)}
    </div>
  )
}

/** Native <dialog>: focus trap, Esc and backdrop come for free. */
export function Modal({
  open,
  onClose,
  title,
  children,
  className = 'max-w-md',
}: {
  open: boolean
  onClose: () => void
  title: string
  children: React.ReactNode
  className?: string
}) {
  const ref = useRef<HTMLDialogElement>(null)
  // The 'close' event of a close() we made arrives a task later; if the modal was reopened by then (e.g. the 'c'
  // shortcut right after submitting), forwarding it would shut the new one. Only user closes reach onClose.
  const selfClosed = useRef(false)
  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (open && !d.open) d.showModal()
    if (!open && d.open) {
      selfClosed.current = true
      d.close()
    }
  }, [open])
  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: backdrop click is a mouse nicety, Esc closes natively
    <dialog
      ref={ref}
      onClose={() => {
        if (selfClosed.current) selfClosed.current = false
        else onClose()
      }}
      onClick={(e) => e.target === ref.current && onClose()}
      aria-label={title}
      className={`m-auto w-full ${className} rounded-lg border border-line p-0 shadow-xl`}
    >
      {open && (
        <div className="p-4">
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-[15px] font-semibold">{title}</h2>
            <button type="button" onClick={onClose} aria-label="Close" className="rounded p-1 hover:bg-hover">
              <X size={16} />
            </button>
          </div>
          {children}
        </div>
      )}
    </dialog>
  )
}

const tones = {
  ok: 'bg-ok-bg text-ok',
  warn: 'bg-warn-bg text-warn',
  bad: 'bg-bad-bg text-bad',
  neutral: 'bg-hover text-muted',
}

export function Pill({
  tone = 'neutral',
  children,
}: {
  tone?: keyof typeof tones
  children: React.ReactNode
}) {
  return <span className={`rounded-full px-2 py-0.5 text-[12px] font-medium ${tones[tone]}`}>{children}</span>
}

export function Avatar({ name, size = 24 }: { name?: string | null; size?: number }) {
  const t = (name ?? '?')
    .split(/\s+/)
    .map((p) => p[0])
    .slice(0, 2)
    .join('')
    .toUpperCase()
  return (
    <span
      title={name ?? 'Unassigned'}
      style={{ width: size, height: size, fontSize: size * 0.42 }}
      className="inline-flex shrink-0 items-center justify-center rounded-full bg-accent font-semibold text-accent-fg"
    >
      {t}
    </span>
  )
}

export function FreshnessPill({ freshness, verifyBy }: { freshness: string; verifyBy: string | null }) {
  if (freshness === 'fresh') return <Pill tone="ok">{verifyBy ? `Fresh until ${verifyBy}` : 'Fresh'}</Pill>
  if (freshness === 'stale') return <Pill tone="warn">Needs review</Pill>
  if (freshness === 'archived') return <Pill>Archived</Pill>
  return <Pill tone="warn">Unverified</Pill>
}

export function ErrorText({ error }: { error?: Error | string | null }) {
  if (!error) return null
  return (
    <p role="alert" className="text-[13px] text-bad">
      {typeof error === 'string' ? error : error.message}
    </p>
  )
}

/** Quiet empty state: icon tile, short title, one-line hint, optional actions. `compact` for cards and side panels. */
export function EmptyState({
  icon: Icon,
  title,
  children,
  action,
  compact,
}: {
  icon: LucideIcon
  title: string
  children?: React.ReactNode
  action?: React.ReactNode
  compact?: boolean
}) {
  return (
    <div
      className={`mx-auto flex flex-col items-center text-center ${compact ? 'gap-1.5 py-5' : 'gap-2 py-14'}`}
    >
      <span
        aria-hidden="true"
        className={`mb-1 flex items-center justify-center rounded-xl border border-line bg-hover text-muted ${compact ? 'h-8 w-8' : 'h-11 w-11'}`}
      >
        <Icon size={compact ? 16 : 20} strokeWidth={1.75} />
      </span>
      <p className="text-[14px] font-semibold">{title}</p>
      {children ? <div className="max-w-[42ch] text-[13px] text-muted">{children}</div> : null}
      {action ? <div className="mt-2 flex flex-wrap items-center justify-center gap-2">{action}</div> : null}
    </div>
  )
}
