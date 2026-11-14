'use client'
import { useSearchParams } from 'next/navigation'
import { TicketList } from '@/components/crm/tickets'

export default function Page() {
  return <TicketList initialBreached={useSearchParams().get('filter') === 'breached'} />
}
