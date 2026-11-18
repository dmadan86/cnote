'use client'
import { DashboardList } from '@/components/reports/dashboards'
import { ReportsNav } from '@/components/reports/library'

export default function Page() {
  return (
    <>
      <h1 className="mb-3 text-xl font-semibold">Reports</h1>
      <ReportsNav />
      <DashboardList />
    </>
  )
}
