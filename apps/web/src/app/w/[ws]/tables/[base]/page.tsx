'use client'
import { use } from 'react'
import { BasePage } from '@/components/tables/base-page'

export default function Page({ params }: { params: Promise<{ base: string }> }) {
  const { base } = use(params)
  return <BasePage key={base} baseId={base} />
}
