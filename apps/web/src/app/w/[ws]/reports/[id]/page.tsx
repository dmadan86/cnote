'use client'
import { use } from 'react'
import { Builder } from '@/components/reports/builder'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <Builder id={use(params).id} />
}
