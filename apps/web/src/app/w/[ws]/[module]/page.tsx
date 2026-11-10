'use client'
import { use } from 'react'
import { useShell } from '@/components/shell'
import { MODULES } from '@/lib/modules'

export default function Soon({ params }: { params: Promise<{ module: string }> }) {
  const { module } = use(params)
  useShell()
  const m = MODULES.find((x) => x.key === module)
  if (!m) return <p className="p-6 text-muted">Page not found.</p>
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
      <m.icon size={32} className="text-muted" />
      <h1 className="text-lg font-semibold">{m.label}</h1>
      <p className="text-muted">Coming soon.</p>
    </div>
  )
}
