'use client'
import { use } from 'react'
import { PayslipView } from '@/components/hr/payroll'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <PayslipView id={use(params).id} />
}
