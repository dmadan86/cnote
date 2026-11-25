'use client'
import { Plus, Settings } from 'lucide-react'
import Link from 'next/link'
import { use, useEffect, useState } from 'react'
import { DocxImportButton } from '@/components/import/docx-button'
import { PageList } from '@/components/page-list'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import type { SpaceB1 } from '@/lib/docs'

export default function SpacePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws, spaces, notes, newPage } = useShell()
  // An archived space is not in the shell's list but stays readable, so the page asks for it by itself.
  const [space, setSpace] = useState<SpaceB1>()
  const [missing, setMissing] = useState(false)
  // biome-ignore lint/correctness/useExhaustiveDependencies: refetch when the shell's list changes (settings saved)
  useEffect(() => {
    cmd<SpaceB1[]>(ws, 'space.list', { include_archived: true })
      .then((all) => {
        const s = all.find((x) => x.id === id)
        setSpace(s)
        setMissing(!s)
      })
      .catch(() => setMissing(true))
  }, [ws, id, spaces])
  if (missing) return <p className="p-6 text-muted">Space not found.</p>
  if (!space) return null
  return (
    <div className="mx-auto max-w-3xl space-y-4 px-6 py-8">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold">{space.name}</h1>
        <Pill>{space.key}</Pill>
        <Pill>{space.visibility}</Pill>
        <div className="ml-auto flex gap-2">
          {space.my_role === 'admin' && (
            <Link href={`/w/${ws}/s/${id}/settings`} className={btn}>
              <Settings size={14} /> Space settings
            </Link>
          )}
          {!space.archived && space.my_role !== 'viewer' && <DocxImportButton spaceId={id} />}
          {!space.archived && (
            <button type="button" onClick={() => newPage({ spaceId: id })} className={btnPrimary}>
              <Plus size={14} /> New page
            </button>
          )}
        </div>
      </div>
      {space.archived && (
        <p role="status" className="rounded-md bg-warn-bg px-3 py-2 text-[13px] text-warn">
          This space is archived. It is read-only.
        </p>
      )}
      {space.description && <p className="text-[14px] text-muted">{space.description}</p>}
      <PageList
        pages={notes.filter((n) => n.space_id === id && n.types.length === 0)}
        empty="No pages in this space yet."
      />
    </div>
  )
}
