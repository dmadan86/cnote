'use client'
import Link from 'next/link'
import { use } from 'react'
import type { Item } from '@/components/projects/shared'
import { useShell } from '@/components/shell'
import { Avatar, Pill } from '@/components/ui'
import { useCmd } from '@/lib/hooks'

/** A workspace member's profile: who they are and the open work assigned to them. Also where @mentions link. */
export default function PersonPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params)
  const { ws, members, me } = useShell()
  const m = members.find((x) => x.user_id === id)
  const open = useCmd<Item[]>(
    ws,
    'item.list',
    { cnql: `assignee = "${id}" AND statusCategory != Done`, limit: 50 },
    !!m,
  )
  if (!m) return <p className="p-6 text-muted">This person is not a member of this workspace.</p>
  return (
    <div className="mx-auto max-w-3xl space-y-8 px-6 py-10">
      <header className="flex items-center gap-4">
        <Avatar name={m.name} size={64} />
        <div className="min-w-0">
          <h1 className="truncate text-2xl font-bold">
            {m.name}
            {m.user_id === me.user.id && (
              <span className="ml-2 text-[13px] font-normal text-muted">(you)</span>
            )}
          </h1>
          <p className="text-muted">
            <a href={`mailto:${m.email}`} className="hover:underline">
              {m.email}
            </a>{' '}
            · <span className="capitalize">{m.role}</span>
          </p>
        </div>
      </header>
      <section aria-label="Assigned work">
        <h2 className="mb-2 text-[13px] font-semibold">Open work assigned</h2>
        {open.data?.length === 0 && <p className="text-[13px] text-muted">Nothing open.</p>}
        <ul className="divide-y divide-line rounded-md border border-line">
          {open.data?.map((it) => (
            <li key={it.id}>
              <Link
                href={`/w/${ws}/projects/${it.key}`}
                className="flex items-center gap-3 px-3 py-2 hover:bg-hover"
              >
                <span className="w-16 shrink-0 text-[12px] text-muted">{it.key}</span>
                <span className="min-w-0 flex-1 truncate">{it.title}</span>
                <Pill tone={it.status_category === 'in_progress' ? 'warn' : 'neutral'}>{it.status}</Pill>
              </Link>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}
