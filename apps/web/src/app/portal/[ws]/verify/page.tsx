'use client'
import { use } from 'react'
import { Verify } from '@/components/portal'

export default function Page({ params }: { params: Promise<{ ws: string }> }) {
  return <Verify ws={use(params).ws} />
}
