'use client'
import { useShell } from '@/components/shell'
import { Builder } from '@/components/sites/builder'
import type { Support } from '@/components/sites/help'
import { Start } from '@/components/sites/start'
import { ErrorText } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import type { Site } from '@/lib/sites'

export function SiteHome({ domain, support }: { domain?: string; support: Support }) {
  const { ws, me } = useShell()
  const role = me.workspaces.find((w) => w.id === ws)?.role
  const canAdmin = role === 'admin' || role === 'owner'
  const sites = useCmd<Site[]>(ws, 'site.list')
  if (sites.error) return <ErrorText error={sites.error} />
  if (!sites.data) return <p className="p-6 text-[13px] text-muted">Loading…</p>
  // A workspace may hold several sites; the newest opens.
  return sites.data.length ? (
    <Builder ws={ws} siteId={sites.data[0].id} canAdmin={canAdmin} domain={domain} support={support} />
  ) : (
    <Start ws={ws} canCreate={canAdmin} onCreated={sites.reload} />
  )
}
