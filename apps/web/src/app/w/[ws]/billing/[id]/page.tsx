'use client'
import { use } from 'react'
import { Editor } from '@/components/billing/editor'
import type { InvoiceDetail } from '@/components/billing/fmt'
import { IssuedView } from '@/components/billing/view'
import { useShell } from '@/components/shell'
import { ErrorText } from '@/components/ui'
import { useCmd } from '@/lib/hooks'

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws } = useShell()
  const { data, error, reload } = useCmd<InvoiceDetail>(ws, 'invoice.get', { id })
  if (error) return <ErrorText error={error} />
  if (!data) return <p className="text-muted">Loading...</p>
  return data.status === 'draft' ? (
    <Editor key={data.id} inv={data} onIssued={reload} />
  ) : (
    <IssuedView inv={data} reload={reload} />
  )
}
