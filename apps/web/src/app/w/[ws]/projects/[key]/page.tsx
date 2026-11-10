'use client'
import { use } from 'react'
import { ItemDetailView } from '@/components/projects/item-detail'
import { SpaceView } from '@/components/projects/space-view'
import { useShell } from '@/components/shell'

/** One dynamic segment serves both: a space key (ENG) or an item key (ENG-12). */
export default function Page({ params }: { params: Promise<{ key: string }> }) {
  const { key } = use(params)
  const { spaces } = useShell()
  if (/-\d+$/.test(key)) return <ItemDetailView key={key} itemKey={key} />
  const space = spaces.find((s) => s.key === key.toUpperCase())
  if (!space) return <p className="p-6 text-muted">Project not found.</p>
  return <SpaceView key={space.id} space={space} />
}
