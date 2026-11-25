'use client'
import { useRouter } from 'next/navigation'
import { use } from 'react'
import { SpaceSettingsDialog } from '@/components/docs/space-settings'
import { useShell } from '@/components/shell'

export default function SpaceSettingsPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws } = useShell()
  const router = useRouter()
  return <SpaceSettingsDialog spaceId={id} open onClose={() => router.push(`/w/${ws}/s/${id}`)} />
}
