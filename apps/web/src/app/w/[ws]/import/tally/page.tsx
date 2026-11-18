'use client'
import { Suspense } from 'react'
import { ImportWizard } from '@/components/import/wizard'

export default function TallyImportPage() {
  return (
    <Suspense>
      <ImportWizard kind="tally" />
    </Suspense>
  )
}
