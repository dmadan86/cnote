'use client'
import { useEffect } from 'react'
import { ImportForm } from '@/components/import/import-form'
import { JobList } from '@/components/import/job-list'
import type { Job } from '@/components/import/types'
import { useShell } from '@/components/shell'
import { useCmd } from '@/lib/hooks'

export default function ImportPage() {
  const { ws } = useShell()
  const jobs = useCmd<Job[]>(ws, 'import.list')
  const live = jobs.data?.some((j) => j.status === 'queued' || j.status === 'running')
  const { reload } = jobs
  useEffect(() => {
    if (!live) return
    const t = setInterval(reload, 1500)
    return () => clearInterval(t)
  }, [live, reload])
  return (
    <div className="mx-auto max-w-3xl space-y-8 px-6 py-8">
      <h1 className="text-xl font-semibold">Import</h1>
      <ImportForm ws={ws} onStarted={reload} />
      <JobList jobs={jobs.data} error={jobs.error} />
    </div>
  )
}
