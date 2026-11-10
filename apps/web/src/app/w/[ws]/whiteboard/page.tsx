'use client'
import { Palette, Presentation } from 'lucide-react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, inputCls } from '@/components/ui'
import { cmd, type Note } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'

export default function WhiteboardIndex() {
  const { ws, spaces, openSpaceDialog } = useShell()
  const router = useRouter()
  const [spaceId, setSpaceId] = useState('')
  const [err, setErr] = useState<string>()
  const boards = useCmd<Note[]>(ws, 'note.list', { type: 'whiteboard' })
  const designs = useCmd<Note[]>(ws, 'note.list', { type: 'design' })
  const space = spaceId || spaces[0]?.id

  async function create(type: 'whiteboard' | 'design') {
    if (!space) return openSpaceDialog()
    try {
      const n = await cmd<Note>(ws, 'note.create', {
        space_id: space,
        title: type === 'design' ? 'Untitled design' : 'Untitled whiteboard',
        types: [type],
      })
      router.push(`/w/${ws}/whiteboard/${n.id}`)
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const list = (title: string, q: typeof boards) => (
    <section aria-label={title} className="space-y-2">
      <h2 className="text-[13px] font-medium text-muted">{title}</h2>
      {q.error && <ErrorText error={q.error} />}
      {q.data?.length === 0 && <p className="text-muted">None yet.</p>}
      <ul className="divide-y divide-line rounded-lg border border-line">
        {q.data?.map((n) => (
          <li key={n.id}>
            <Link
              href={`/w/${ws}/whiteboard/${n.id}`}
              className="flex justify-between px-3 py-2 hover:bg-hover"
            >
              <span>{n.title}</span>
              <span className="text-muted">{ago(n.updated_at)}</span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )

  return (
    <div className="mx-auto max-w-3xl space-y-6 px-6 py-8">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">Whiteboard</h1>
        <div className="ml-auto flex items-center gap-2">
          <select
            aria-label="Space"
            className={`${inputCls} w-auto`}
            value={space ?? ''}
            onChange={(e) => setSpaceId(e.target.value)}
          >
            {spaces.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
          <button type="button" className={btnPrimary} onClick={() => create('whiteboard')}>
            <Presentation size={14} /> New whiteboard
          </button>
          <button type="button" className={btn} onClick={() => create('design')}>
            <Palette size={14} /> New design
          </button>
        </div>
      </div>
      <ErrorText error={err} />
      {list('Whiteboards', boards)}
      {list('Designs', designs)}
    </div>
  )
}
