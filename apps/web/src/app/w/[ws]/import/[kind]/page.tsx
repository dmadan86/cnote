'use client'
import { notFound } from 'next/navigation'
import { Suspense, use } from 'react'
import { ImportWizard } from '@/components/import/wizard'

const KINDS = { contacts: 'contacts', items: 'items', 'opening-docs': 'opening_docs' } as const

export default function WizardPage({ params }: { params: Promise<{ kind: string }> }) {
  const { kind } = use(params)
  const k = KINDS[kind as keyof typeof KINDS]
  if (!k) notFound()
  return (
    <Suspense>
      <ImportWizard kind={k} />
    </Suspense>
  )
}
