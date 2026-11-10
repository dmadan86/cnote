'use client'
import { use } from 'react'
import { DealDetail } from '@/components/crm/deals'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <DealDetail id={use(params).id} />
}
