'use client'
import Link from 'next/link'
import { useEffect, useRef, useState } from 'react'
import type { Member } from '@/lib/api'
import { useShell } from './shell'
import { Avatar } from './ui'

// Stored like page mentions, so the server notifies and links them the same way.
const LINK = /\[@([^\]]+)\]\((\/w\/[0-9a-f-]{36}\/people\/[0-9a-f-]{36})\)/g

/** Comment text with @mentions shown as links (hover shows the person's card). */
export function CommentBody({ body }: { body: string }) {
  const parts: React.ReactNode[] = []
  let last = 0
  for (const m of body.matchAll(LINK)) {
    parts.push(body.slice(last, m.index))
    parts.push(
      <Link key={m.index} href={m[2]} className="font-medium text-accent hover:underline">
        @{m[1]}
      </Link>,
    )
    last = (m.index ?? 0) + m[0].length
  }
  parts.push(body.slice(last))
  return <>{parts}</>
}

/**
 * A comment box with an @ picker. People see "@Asha Rao"; the hidden field named `name` carries the stored form
 * "[@Asha Rao](/w/<ws>/people/<id>)", so forms keep reading FormData as before.
 */
export function MentionField({
  name,
  multiline,
  className,
  ...rest
}: { name: string; multiline?: boolean; className?: string; rows?: number } & Omit<
  React.InputHTMLAttributes<HTMLElement>,
  'name' | 'value' | 'onChange'
>) {
  const { ws, members } = useShell()
  const [text, setText] = useState('')
  const [picked, setPicked] = useState<Member[]>([])
  const [query, setQuery] = useState<{ q: string; at: number } | null>(null)
  const [active, setActive] = useState(0)
  const ref = useRef<HTMLTextAreaElement & HTMLInputElement>(null)

  // form.reset() does not reach controlled fields: clear on the form's reset event.
  useEffect(() => {
    const f = ref.current?.form
    const clear = () => {
      setText('')
      setPicked([])
      setQuery(null)
    }
    f?.addEventListener('reset', clear)
    return () => f?.removeEventListener('reset', clear)
  }, [])

  const options = query
    ? members.filter((m) => `${m.name} ${m.email}`.toLowerCase().includes(query.q.toLowerCase())).slice(0, 6)
    : []

  const stored = picked.reduce(
    (s, m) => s.split(`@${m.name}`).join(`[@${m.name}](/w/${ws}/people/${m.user_id})`),
    text,
  )

  function onChange(v: string, caret: number) {
    setText(v)
    const m = /(^|\s)@([^\s@]{0,30}(?: [^\s@]{0,30})?)$/.exec(v.slice(0, caret))
    setQuery(m ? { q: m[2], at: caret - m[2].length - 1 } : null)
    setActive(0)
  }

  function pick(m: Member) {
    if (!query) return
    const caret = query.at + 1 + query.q.length
    const v = `${text.slice(0, query.at)}@${m.name} ${text.slice(caret)}`
    setText(v)
    setPicked((p) => (p.some((x) => x.user_id === m.user_id) ? p : [...p, m]))
    setQuery(null)
    requestAnimationFrame(() => {
      const pos = query.at + m.name.length + 2
      ref.current?.focus()
      ref.current?.setSelectionRange(pos, pos)
    })
  }

  const props = {
    ...rest,
    ref,
    value: text,
    className,
    'aria-autocomplete': 'list' as const,
    'aria-expanded': options.length > 0,
    onChange: (e: React.ChangeEvent<HTMLTextAreaElement | HTMLInputElement>) =>
      onChange(e.target.value, e.target.selectionStart ?? e.target.value.length),
    onKeyDown: (e: React.KeyboardEvent) => {
      if (!options.length) return
      if (e.key === 'ArrowDown') setActive((a) => (a + 1) % options.length)
      else if (e.key === 'ArrowUp') setActive((a) => (a + options.length - 1) % options.length)
      else if (e.key === 'Enter' || e.key === 'Tab') pick(options[active])
      else if (e.key === 'Escape') setQuery(null)
      else return
      e.preventDefault()
    },
  }
  return (
    <div className="relative">
      {multiline ? <textarea {...props} /> : <input {...props} />}
      <input type="hidden" name={name} value={stored} />
      {options.length > 0 && (
        <div
          role="listbox"
          aria-label="Mention someone"
          className="absolute left-0 z-30 mt-1 w-64 rounded-lg border border-line bg-canvas p-1 text-[13px] shadow-xl"
        >
          {options.map((m, i) => (
            <button
              key={m.user_id}
              type="button"
              role="option"
              aria-selected={i === active}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => pick(m)}
              className="flex w-full items-center gap-2 rounded px-2 py-1 text-left aria-selected:bg-hover"
            >
              <Avatar name={m.name} size={20} />
              <span className="truncate">{m.name}</span>
              <span className="ml-auto truncate text-[11px] text-muted">{m.email}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
