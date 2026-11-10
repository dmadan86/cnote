'use client'
import { use } from 'react'
import { useShell } from '@/components/shell'
import { Board } from '@/components/whiteboard/board'
import type { NoteDetail } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

export default function BoardPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws } = useShell()
  const { data: note, error } = useCmd<NoteDetail>(ws, 'note.get', { id })
  if (error)
    return <p className="p-6 text-muted">This board does not exist or you do not have access to it.</p>
  if (!note) return <p className="p-6 text-muted">Loading...</p>
  return (
    <Board
      key={id}
      id={id}
      title={note.title}
      contentMd={note.content_md}
      design={note.types.includes('design')}
    />
  )
}
