'use client'
import { QuoteView } from '@/components/billing/quotes'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <QuoteView params={params} />
}
