'use client'
import { ReceiptView } from '@/components/billing/receipts'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <ReceiptView params={params} />
}
