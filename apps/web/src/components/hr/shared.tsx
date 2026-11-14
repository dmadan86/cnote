'use client'
import Link from 'next/link'
import { useShell } from '@/components/shell'

export const isAdminRole = (role?: string) => role === 'owner' || role === 'admin'

export function useRole() {
  const { ws, me } = useShell()
  return { ws, role: me.workspaces.find((w) => w.id === ws)?.role, meId: me.user.id }
}

/** Tabs shared by the people pages. */
export function PeopleTabs({ current }: { current: 'directory' | 'attendance' | 'leave' }) {
  const { ws } = useShell()
  const tabs = [
    ['directory', 'Directory', `/w/${ws}/people`],
    ['attendance', 'Attendance', `/w/${ws}/people/attendance`],
    ['leave', 'Leave', `/w/${ws}/people/leave`],
  ] as const
  return (
    <nav aria-label="People sections" className="flex gap-1 border-b border-line">
      {tabs.map(([k, label, href]) => (
        <Link
          key={k}
          href={href}
          aria-current={k === current ? 'page' : undefined}
          className="-mb-px border-b-2 border-transparent px-3 py-2 text-[13px] font-medium text-muted hover:text-fg aria-[current=page]:border-accent aria-[current=page]:text-fg"
        >
          {label}
        </Link>
      ))}
    </nav>
  )
}

export const timeOf = (iso?: string | null) =>
  iso
    ? new Date(iso).toLocaleTimeString('en-IN', {
        hour: 'numeric',
        minute: '2-digit',
        timeZone: 'Asia/Kolkata',
      })
    : ''
