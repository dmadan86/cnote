'use client'
import { use } from 'react'
import { Builder } from '@/components/reports/builder'

export default function Page({ searchParams }: { searchParams: Promise<{ template?: string }> }) {
  return <Builder template={use(searchParams).template} />
}
