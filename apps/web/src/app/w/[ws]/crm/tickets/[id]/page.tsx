'use client'
import { use } from 'react'
import { TicketDetail } from '@/components/crm/tickets'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <TicketDetail id={use(params).id} />
}
