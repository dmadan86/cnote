'use client'
import { use } from 'react'
import { ContactDetail } from '@/components/crm/contact-detail'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <ContactDetail id={use(params).id} />
}
