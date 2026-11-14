'use client'
import { use } from 'react'
import { RunDetail } from '@/components/hr/payroll'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <RunDetail id={use(params).id} />
}
