'use client'
import { Plus } from 'lucide-react'
import { use } from 'react'
import { PageList } from '@/components/page-list'
import { useShell } from '@/components/shell'
import { btnPrimary, Pill } from '@/components/ui'

export default function SpacePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { spaces, notes, newPage } = useShell()
  const space = spaces.find((s) => s.id === id)
  if (!space) return <p className="p-6 text-muted">Space not found.</p>
  return (
    <div className="mx-auto max-w-3xl space-y-4 px-6 py-8">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold">{space.name}</h1>
        <Pill>{space.key}</Pill>
        <Pill>{space.visibility}</Pill>
        <button type="button" onClick={() => newPage({ spaceId: id })} className={`${btnPrimary} ml-auto`}>
          <Plus size={14} /> New page
        </button>
      </div>
      <PageList
        pages={notes.filter((n) => n.space_id === id && n.types.length === 0)}
        empty="No pages in this space yet."
      />
    </div>
  )
}
