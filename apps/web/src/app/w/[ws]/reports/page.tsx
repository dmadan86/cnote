'use client'
import { Library, ReportsNav } from '@/components/reports/library'

export default function Page() {
  return (
    <>
      <h1 className="mb-3 text-xl font-semibold">Reports</h1>
      <ReportsNav />
      <Library />
    </>
  )
}
