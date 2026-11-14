'use client'
import { inputCls } from '@/components/ui'
import { useCmd } from '@/lib/hooks'

export type Branch = {
  id: string
  name: string
  address: string | null
  gstin: string | null
  state_code: string | null
  members: number
}

/** Branches of the workspace; empty for guests and for workspaces without any. */
export function useBranches(ws: string) {
  const r = useCmd<Branch[]>(ws, 'branch.list')
  return { branches: r.data ?? [], reload: r.reload }
}

/** "All branches" select; renders nothing until the workspace has a branch. */
export function BranchSelect({
  ws,
  value,
  onChange,
  allLabel = 'All branches',
  name,
}: {
  ws: string
  value: string
  onChange?: (v: string) => void
  allLabel?: string
  name?: string
}) {
  const { branches } = useBranches(ws)
  if (!branches.length) return null
  return (
    <select
      aria-label="Branch"
      name={name}
      className={`${inputCls} w-auto`}
      value={onChange ? value : undefined}
      defaultValue={onChange ? undefined : value}
      onChange={onChange && ((e) => onChange(e.target.value))}
    >
      <option value="">{allLabel}</option>
      {branches.map((b) => (
        <option key={b.id} value={b.id}>
          {b.name}
        </option>
      ))}
    </select>
  )
}
