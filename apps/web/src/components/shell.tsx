'use client'
import { ChevronDown, FileText, Plus, Search, Settings } from 'lucide-react'
import Link from 'next/link'
import { usePathname, useRouter } from 'next/navigation'
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { auth, cmd, type Me, type Member, type Note, type Space } from '@/lib/api'
import { MODULES } from '@/lib/modules'
import { Palette } from './palette'
import { SpaceDialog } from './space-dialog'
import { Avatar, btnPrimary } from './ui'

type Shell = {
  ws: string
  me: Me
  spaces: Space[]
  notes: Note[]
  members: Member[]
  reload: () => void
  newPage: (opts?: { spaceId?: string; parentId?: string }) => Promise<void>
  openSpaceDialog: () => void
  togglePalette: () => void
}

const Ctx = createContext<Shell | null>(null)
export const useShell = () => {
  const c = useContext(Ctx)
  if (!c) throw new Error('outside shell')
  return c
}

/** Dropdown with outside-click and Escape handling. */
function Menu({
  label,
  trigger,
  children,
  align = 'left',
}: {
  label: string
  trigger: React.ReactNode
  children: (close: () => void) => React.ReactNode
  align?: 'left' | 'right'
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const out = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false)
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', out)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', out)
      document.removeEventListener('keydown', esc)
    }
  }, [open])
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 rounded-md px-1.5 py-1 hover:bg-hover"
      >
        {trigger}
      </button>
      {open && (
        <div
          role="menu"
          className={`absolute z-30 mt-1 min-w-52 rounded-md border border-line bg-canvas p-1 shadow-lg ${align === 'right' ? 'right-0' : 'left-0'}`}
        >
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  )
}

const menuItem = 'flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover'

function Tree({
  spaceId,
  notes,
  parent,
  depth,
}: {
  spaceId: string
  notes: Note[]
  parent: string | null
  depth: number
}) {
  const path = usePathname()
  const { ws } = useShell()
  const kids = notes.filter((n) => n.space_id === spaceId && n.parent_id === parent)
  return (
    <ul>
      {kids.map((n) => (
        <li key={n.id}>
          <Link
            href={`/w/${ws}/n/${n.id}`}
            style={{ paddingLeft: 8 + depth * 12 }}
            aria-current={path.endsWith(`/n/${n.id}`) ? 'page' : undefined}
            className="flex items-center gap-1.5 truncate rounded py-1 pr-2 text-[13px] hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:font-medium"
          >
            <FileText size={13} className="shrink-0 text-muted" />
            <span className="truncate">{n.title || 'Untitled'}</span>
          </Link>
          <Tree spaceId={spaceId} notes={notes} parent={n.id} depth={depth + 1} />
        </li>
      ))}
    </ul>
  )
}

function Breadcrumb({ s }: { s: Shell }) {
  const path = usePathname()
  const seg = path.split('/').slice(3)
  const crumbs: { label: string; href?: string }[] = [
    { label: s.me.workspaces.find((w) => w.id === s.ws)?.name ?? '', href: `/w/${s.ws}` },
  ]
  const space = (id: string) => s.spaces.find((x) => x.id === id)
  if (seg[0] === 's') {
    const sp = space(seg[1])
    if (sp) crumbs.push({ label: sp.name })
  } else if (seg[0] === 'n') {
    const chain: Note[] = []
    for (let n = s.notes.find((x) => x.id === seg[1]); n; n = s.notes.find((x) => x.id === n?.parent_id))
      chain.unshift(n)
    const sp = chain[0] && space(chain[0].space_id)
    if (sp) crumbs.push({ label: sp.name, href: `/w/${s.ws}/s/${sp.id}` })
    for (const n of chain) crumbs.push({ label: n.title || 'Untitled', href: `/w/${s.ws}/n/${n.id}` })
  } else if (seg[0]) {
    crumbs.push({ label: seg[0][0].toUpperCase() + seg[0].slice(1) })
  }
  return (
    <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1.5 text-[13px] text-muted">
      {crumbs.map((c, i) => (
        <span key={c.href ?? c.label} className="flex min-w-0 items-center gap-1.5">
          {i > 0 && <span aria-hidden>/</span>}
          {c.href && i < crumbs.length - 1 ? (
            <Link href={c.href} className="truncate hover:text-fg">
              {c.label}
            </Link>
          ) : (
            <span className="truncate text-fg" aria-current={i === crumbs.length - 1 ? 'page' : undefined}>
              {c.label}
            </span>
          )}
        </span>
      ))}
    </nav>
  )
}

export function ShellView({ ws, children }: { ws: string; children: React.ReactNode }) {
  const router = useRouter()
  const path = usePathname()
  const [me, setMe] = useState<Me>()
  const [spaces, setSpaces] = useState<Space[]>([])
  const [notes, setNotes] = useState<Note[]>([])
  const [members, setMembers] = useState<Member[]>([])
  const [denied, setDenied] = useState(false)
  const [tick, setTick] = useState(0)
  const [spaceDialog, setSpaceDialog] = useState(false)
  const [palette, setPalette] = useState(false)

  useEffect(() => {
    auth.me().then(setMe, () => router.replace('/login'))
  }, [router])

  // biome-ignore lint/correctness/useExhaustiveDependencies: tick forces reload
  useEffect(() => {
    if (!me) return
    if (!me.workspaces.some((w) => w.id === ws)) return setDenied(true)
    setDenied(false)
    Promise.all([
      cmd<Space[]>(ws, 'space.list'),
      cmd<Note[]>(ws, 'note.list', { limit: 500 }),
      cmd<Member[]>(ws, 'member.list'),
    ]).then(
      ([s, n, m]) => {
        setSpaces(s)
        setNotes(n)
        setMembers(m)
      },
      () => setDenied(true),
    )
  }, [me, ws, tick])

  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPalette((p) => !p)
      }
    }
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [])

  const reload = useCallback(() => setTick((t) => t + 1), [])

  const currentSpaceId = useMemo(() => {
    const seg = path.split('/').slice(3)
    if (seg[0] === 's') return seg[1]
    if (seg[0] === 'n') return notes.find((n) => n.id === seg[1])?.space_id
  }, [path, notes])

  const newPage = useCallback(
    async (o?: { spaceId?: string; parentId?: string }) => {
      const spaceId = o?.spaceId ?? currentSpaceId ?? spaces[0]?.id
      if (!spaceId) return setSpaceDialog(true)
      const n = await cmd<Note>(ws, 'note.create', {
        space_id: spaceId,
        title: 'Untitled',
        parent_id: o?.parentId,
      })
      reload()
      router.push(`/w/${ws}/n/${n.id}`)
    },
    [ws, currentSpaceId, spaces, reload, router],
  )

  if (!me) return null
  if (denied)
    return (
      <main className="flex min-h-screen flex-col items-center justify-center gap-3">
        <h1 className="text-lg font-semibold">Not found</h1>
        <p className="text-muted">This workspace does not exist or you do not have access to it.</p>
        <Link href="/" className={btnPrimary}>
          Go to your workspace
        </Link>
      </main>
    )

  const shell: Shell = {
    ws,
    me,
    spaces,
    notes,
    members,
    reload,
    newPage,
    openSpaceDialog: () => setSpaceDialog(true),
    togglePalette: () => setPalette((p) => !p),
  }
  const current = me.workspaces.find((w) => w.id === ws)
  const base = `/w/${ws}`
  const hrefFor = (key: string) =>
    key === 'docs' ? (spaces[0] ? `${base}/s/${spaces[0].id}` : base) : key ? `${base}/${key}` : base
  const active = (key: string) =>
    key === '' ? path === base : key === 'docs' ? /\/(s|n)\//.test(path) : path.startsWith(`${base}/${key}`)

  return (
    <Ctx.Provider value={shell}>
      <div className="flex h-screen">
        <nav
          aria-label="Modules"
          className="flex w-12 shrink-0 flex-col items-center gap-1 border-r border-line bg-side py-2"
        >
          {MODULES.map((m) => (
            <Link
              key={m.key}
              href={hrefFor(m.key)}
              aria-label={m.label}
              title={m.label}
              aria-current={active(m.key) ? 'page' : undefined}
              className="rounded-md p-2 text-muted hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:text-fg"
            >
              <m.icon size={18} />
            </Link>
          ))}
          <Link
            href={`${base}/settings`}
            aria-label="Settings"
            title="Settings"
            aria-current={active('settings') ? 'page' : undefined}
            className="mt-auto rounded-md p-2 text-muted hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:text-fg"
          >
            <Settings size={18} />
          </Link>
        </nav>

        <aside aria-label="Spaces" className="flex w-60 shrink-0 flex-col border-r border-line bg-side">
          <div className="p-2">
            <Menu
              label="Switch workspace"
              trigger={
                <>
                  <span className="flex h-5 w-5 items-center justify-center rounded bg-accent text-[11px] font-bold text-accent-fg">
                    {current?.name[0]?.toUpperCase()}
                  </span>
                  <span className="max-w-32 truncate text-[13px] font-semibold">{current?.name}</span>
                  <ChevronDown size={14} className="text-muted" />
                </>
              }
            >
              {(close) => (
                <>
                  {me.workspaces.map((w) => (
                    <Link key={w.id} href={`/w/${w.id}`} onClick={close} role="menuitem" className={menuItem}>
                      {w.name}
                    </Link>
                  ))}
                  <button
                    type="button"
                    role="menuitem"
                    className={menuItem}
                    onClick={async () => {
                      const name = window.prompt('Workspace name')
                      close()
                      if (name?.trim()) router.push(`/w/${(await auth.createWorkspace(name.trim())).id}`)
                    }}
                  >
                    <Plus size={14} /> New workspace
                  </button>
                </>
              )}
            </Menu>
          </div>
          <div className="px-2 pb-2">
            <button type="button" onClick={() => newPage()} className={`${btnPrimary} w-full`}>
              <Plus size={14} /> New page
            </button>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
            <div className="flex items-center justify-between px-2 py-1 text-[11px] font-semibold uppercase tracking-wide text-muted">
              Spaces
              <button
                type="button"
                aria-label="New space"
                onClick={() => setSpaceDialog(true)}
                className="rounded p-0.5 hover:bg-hover"
              >
                <Plus size={13} />
              </button>
            </div>
            {spaces.length === 0 && <p className="px-2 text-[13px] text-muted">No spaces yet.</p>}
            {spaces.map((s) => (
              <div key={s.id} className="mb-1">
                <Link
                  href={`${base}/s/${s.id}`}
                  className="flex items-center gap-1.5 rounded px-2 py-1 text-[13px] font-medium hover:bg-hover"
                >
                  <span className="rounded bg-hover px-1 text-[10px] font-bold text-muted">{s.key}</span>
                  <span className="truncate">{s.name}</span>
                </Link>
                <Tree spaceId={s.id} notes={notes} parent={null} depth={1} />
              </div>
            ))}
          </div>
        </aside>

        <div className="flex min-w-0 flex-1 flex-col">
          <header className="flex h-11 shrink-0 items-center gap-3 border-b border-line px-4">
            <div className="min-w-0 flex-1">
              <Breadcrumb s={shell} />
            </div>
            <button
              type="button"
              onClick={() => setPalette(true)}
              className="flex w-64 items-center gap-2 rounded-md border border-line px-2.5 py-1 text-[13px] text-muted hover:bg-hover"
            >
              <Search size={14} /> Search or jump to...
              <kbd className="ml-auto rounded border border-line px-1 text-[11px]">⌘K</kbd>
            </button>
            <Menu label="User menu" align="right" trigger={<Avatar name={me.user.name} />}>
              {(close) => (
                <>
                  <div className="px-2 py-1.5 text-[12px] text-muted">
                    {me.user.name}
                    <br />
                    {me.user.email}
                  </div>
                  <Link href={`${base}/settings`} onClick={close} role="menuitem" className={menuItem}>
                    Settings
                  </Link>
                  <button
                    type="button"
                    role="menuitem"
                    className={menuItem}
                    onClick={async () => {
                      await auth.logout()
                      router.push('/login')
                    }}
                  >
                    Log out
                  </button>
                </>
              )}
            </Menu>
          </header>
          <main className="min-h-0 flex-1 overflow-y-auto">{children}</main>
        </div>
      </div>
      <SpaceDialog open={spaceDialog} onClose={() => setSpaceDialog(false)} />
      <Palette open={palette} onClose={() => setPalette(false)} />
    </Ctx.Provider>
  )
}
