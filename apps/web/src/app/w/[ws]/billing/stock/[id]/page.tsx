'use client'
import { StockDetail } from '@/components/billing/stock'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <StockDetail params={params} />
}
