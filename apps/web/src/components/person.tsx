'use client'
import { computePosition, flip, offset, shift } from '@floating-ui/dom'
import Link from 'next/link'
import { useEffect, useRef, useState } from 'react'
import type { Member } from '@/lib/api'
import { useShell } from './shell'
import { Avatar } from './ui'

// The id after /people/ (the URL also starts with the workspace id).
const PERSON_HREF = /\/people\/([0-9a-f-]{36})/
const SHOW_MS = 350
const HIDE_MS = 200

/** Spread on anything that names a person (an avatar, a name): hovering or focusing it shows their profile card. */
export const personProps = (userId?: string | null) =>
  userId ? { 'data-person-id': userId, tabIndex: 0, className: 'cursor-default' } : {}

/** The person an element points at: `data-person-id`, or an @mention link to /w/<ws>/people/<id>. */
function personOf(el: Element | null): { id: string; el: HTMLElement } | null {
  const hit = el?.closest<HTMLElement>('[data-person-id], a[href*="/people/"]')
  if (!hit) return null
  const id = hit.dataset.personId ?? hit.getAttribute('href')?.match(PERSON_HREF)?.[1]
  return id ? { id, el: hit } : null
}

export function PersonCard({ ws, m }: { ws: string; m: Member }) {
  return (
    <div className="flex gap-3">
      <Avatar name={m.name} size={40} />
      <div className="min-w-0">
        <p className="truncate font-semibold">{m.name}</p>
        <p className="truncate text-[12px] text-muted">{m.email}</p>
        <p className="mt-0.5 text-[12px] capitalize text-muted">{m.role}</p>
        <div className="mt-2 flex gap-3 text-[12px]">
          <Link href={`/w/${ws}/people/${m.user_id}`} className="text-accent hover:underline">
            View profile
          </Link>
          <a href={`mailto:${m.email}`} className="text-accent hover:underline">
            Email
          </a>
        </div>
      </div>
    </div>
  )
}

/** One listener for the whole app: shows a person's card on hover/focus of any person reference. */
export function PersonHoverLayer() {
  const { ws, members } = useShell()
  const [shown, setShown] = useState<{ id: string; el: HTMLElement } | null>(null)
  const card = useRef<HTMLDivElement>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)

  useEffect(() => {
    const later = (fn: () => void, ms: number) => {
      clearTimeout(timer.current)
      timer.current = setTimeout(fn, ms)
    }
    const enter = (e: Event) => {
      if (card.current?.contains(e.target as Node)) return clearTimeout(timer.current) // keep it while on the card
      const p = personOf(e.target as Element)
      if (p) later(() => setShown(p), e.type === 'focusin' ? 0 : SHOW_MS)
    }
    const leave = (e: Event) => {
      const to = (e as MouseEvent).relatedTarget as Node | null
      if (to && (card.current?.contains(to) || personOf(to as Element))) return
      if (personOf(e.target as Element) || card.current?.contains(e.target as Node))
        later(() => setShown(null), HIDE_MS)
    }
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setShown(null)
    document.addEventListener('mouseover', enter)
    document.addEventListener('focusin', enter)
    document.addEventListener('mouseout', leave)
    document.addEventListener('focusout', leave)
    document.addEventListener('keydown', esc)
    return () => {
      clearTimeout(timer.current)
      document.removeEventListener('mouseover', enter)
      document.removeEventListener('focusin', enter)
      document.removeEventListener('mouseout', leave)
      document.removeEventListener('focusout', leave)
      document.removeEventListener('keydown', esc)
    }
  }, [])

  useEffect(() => {
    const el = card.current
    if (!shown || !el) return
    computePosition(shown.el, el, {
      strategy: 'fixed',
      placement: 'bottom-start',
      middleware: [offset(6), flip(), shift({ padding: 8 })],
    }).then(({ x, y }) => Object.assign(el.style, { left: `${x}px`, top: `${y}px` }))
  }, [shown])

  const m = shown && members.find((x) => x.user_id === shown.id)
  if (!shown || !m) return null
  return (
    <div
      ref={card}
      role="dialog"
      aria-label={`Profile: ${m.name}`}
      style={{ position: 'fixed', left: 0, top: 0 }}
      className="z-50 w-72 rounded-lg border border-line bg-canvas p-3 text-[13px] text-fg shadow-xl"
    >
      <PersonCard ws={ws} m={m} />
    </div>
  )
}
