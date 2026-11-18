import type { DataKind } from './types'

export const KIND_LABEL: Record<DataKind, string> = {
  contacts: 'Contacts',
  items: 'Items',
  opening_docs: 'Unpaid invoices and bills',
  tally: 'Tally data',
}

/** Raw upload (CSV or Tally XML) -> a job in `preview`. Same-origin so the session cookie rides along. */
export async function uploadFile(ws: string, kind: DataKind, file: File): Promise<{ id: string }> {
  const res = await fetch(`/api/w/${ws}/imports?kind=${kind}`, {
    method: 'POST',
    body: file,
    credentials: 'same-origin',
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data?.error?.message ?? res.statusText)
  return data
}

export function saveText(text: string, filename: string, type = 'text/csv') {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([text], { type }))
  a.download = filename
  a.click()
  URL.revokeObjectURL(a.href)
}

export const jobHref = (ws: string, j: { id: string; kind: DataKind }) =>
  `/w/${ws}/import/${j.kind === 'opening_docs' ? 'opening-docs' : j.kind}?job=${j.id}`
