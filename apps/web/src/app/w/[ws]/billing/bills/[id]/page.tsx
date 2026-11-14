'use client'
import { BillView } from '@/components/billing/bills'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <BillView params={params} />
}
