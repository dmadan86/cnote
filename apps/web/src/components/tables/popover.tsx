'use client'
import { useEffect, useId, useRef, useState } from 'react'
import { btn } from '@/components/ui'

/** Button + floating panel. Closes on outside click and Escape (focus returns to the button). */
export function Popover({
  label,
  trigger,
  children,
  className = 'w-72',
  active,
}: {
  label: string
  trigger: React.ReactNode
  children: (close: () => void) => React.ReactNode
  className?: string
  active?: boolean
}) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const id = useId()
  useEffect(() => {
    if (!open) return
    const down = (e: MouseEvent) => !root.current?.contains(e.target as Node) && setOpen(false)
    const key = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      setOpen(false)
      root.current?.querySelector<HTMLButtonElement>('button[aria-haspopup]')?.focus()
    }
    document.addEventListener('mousedown', down)
    document.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('mousedown', down)
      document.removeEventListener('keydown', key)
    }
  }, [open])
  return (
    <div ref={root} className="relative">
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-label={label}
        onClick={() => setOpen((o) => !o)}
        className={`${btn} ${active ? 'bg-hover' : ''}`}
      >
        {trigger}
      </button>
      {open && (
        <div
          id={id}
          role="dialog"
          aria-label={label}
          className={`absolute left-0 z-30 mt-1 max-w-[calc(100vw-2rem)] rounded-lg border border-line bg-canvas p-3 shadow-xl ${className}`}
        >
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  )
}
