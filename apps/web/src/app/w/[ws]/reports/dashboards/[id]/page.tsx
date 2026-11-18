'use client'
import { use } from 'react'
import { DashboardView } from '@/components/reports/dashboards'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <DashboardView id={use(params).id} />
}
