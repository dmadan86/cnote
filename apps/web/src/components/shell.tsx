'use client'
import { ChevronDown, ChevronRight, FileText, Lock, Plus, Search, Settings, Store } from 'lucide-react'
import Link from 'next/link'
import { usePathname, useRouter } from 'next/navigation'
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { auth, cmd, type Me, type Member, type Note, type SharedNote, type Space } from '@/lib/api'
import { MODULES, moduleOn } from '@/lib/modules'
import { NotificationBell } from './notifications'
import { Palette } from './palette'
import { SpaceDialog } from './space-dialog'
import { Avatar, btnPrimary } from './ui'

type Shell = {
  ws: string
  me: Me
  spaces: Space[]
  notes: Note[]
  shared: SharedNote[]
  members: Member[]
  /** settings.modules; null = all enabled. */
  modules: string[] | null
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

type Fold = { isOpen: (id: string, dflt: boolean) => boolean; toggle: (id: string, dflt: boolean) => void }
const FoldCtx = createContext<Fold>({ isOpen: () => true, toggle: () => {} })
const FOLD_KEY = 'cn.tree.fold'

/** Sidebar fold state: user choices persist per browser; pages start folded except the path to the open page. */
function useFold(notes: Note[], currentId: string | undefined): Fold {
  const [over, setOver] = useState<Record<string, boolean>>({})
  useEffect(() => {
    try {
      setOver(JSON.parse(localStorage.getItem(FOLD_KEY) ?? '{}'))
    } catch {}
  }, [])
  const onPath = useMemo(() => {
    const ids = new Set<string>()
    for (let n = notes.find((x) => x.id === currentId); n; n = notes.find((x) => x.id === n?.parent_id))
      ids.add(n.id)
    return ids
  }, [notes, currentId])
  const isOpen = useCallback(
    (id: string, dflt: boolean) => over[id] ?? (dflt || onPath.has(id)),
    [over, onPath],
  )
  const toggle = useCallback(
    (id: string, dflt: boolean) =>
      setOver((o) => {
        const next = { ...o, [id]: !(o[id] ?? (dflt || onPath.has(id))) }
        try {
          localStorage.setItem(FOLD_KEY, JSON.stringify(next))
        } catch {}
        return next
      }),
    [onPath],
  )
  return useMemo(() => ({ isOpen, toggle }), [isOpen, toggle])
}

function FoldButton({ id, label, open, dflt }: { id: string; label: string; open: boolean; dflt: boolean }) {
  const { toggle } = useContext(FoldCtx)
  return (
    <button
      type="button"
      aria-label={`${open ? 'Collapse' : 'Expand'} ${label}`}
      aria-expanded={open}
      onClick={() => toggle(id, dflt)}
      className="shrink-0 rounded p-0.5 text-muted hover:bg-line hover:text-fg"
    >
      <ChevronRight size={12} className={`transition-transform ${open ? 'rotate-90' : ''}`} />
    </button>
  )
}

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
  const fold = useContext(FoldCtx)
  const kids = notes.filter((n) => n.space_id === spaceId && n.parent_id === parent)
  return (
    <ul>
      {kids.map((n) => {
        const hasKids = notes.some((c) => c.parent_id === n.id)
        const open = hasKids && fold.isOpen(n.id, false)
        const current = path.endsWith(`/n/${n.id}`)
        return (
          <li key={n.id}>
            <div
              style={{ paddingLeft: depth * 12 - 8 }}
              className={`flex items-center rounded hover:bg-hover ${current ? 'bg-hover font-medium' : ''}`}
            >
              {hasKids ? (
                <FoldButton id={n.id} label={n.title || 'Untitled'} open={open} dflt={false} />
              ) : (
                <span className="w-4 shrink-0" />
              )}
              <Link
                href={`/w/${ws}/n/${n.id}`}
                aria-current={current ? 'page' : undefined}
                className="flex min-w-0 flex-1 items-center gap-1.5 py-1 pr-2 pl-0.5 text-[13px]"
              >
                <FileText size={13} className="shrink-0 text-muted" />
                <span className="truncate">{n.title || 'Untitled'}</span>
                {n.restriction !== 'none' && (
                  <Lock size={11} className="shrink-0 text-muted" aria-label="Restricted page" />
                )}
              </Link>
            </div>
            {open && <Tree spaceId={spaceId} notes={notes} parent={n.id} depth={depth + 1} />}
          </li>
        )
      })}
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

/** Pages shared to me from spaces I cannot read, plus their sub-pages (a few levels deep) for the tree. */
async function loadShared(ws: string) {
  const roots = await cmd<SharedNote[]>(ws, 'note.shared_with_me')
  const extra: Note[] = []
  let frontier = roots.map((r) => r.id)
  for (let depth = 0; depth < 4 && frontier.length; depth++) {
    const kids = (
      await Promise.all(frontier.map((id) => cmd<Note[]>(ws, 'note.list', { parent_id: id, limit: 500 })))
    ).flat()
    extra.push(...kids)
    frontier = kids.map((k) => k.id)
  }
  return { roots, extra: extra.filter((k) => !roots.some((r) => r.id === k.id)) }
}

export function ShellView({ ws, children }: { ws: string; children: React.ReactNode }) {
  const router = useRouter()
  const path = usePathname()
  const [me, setMe] = useState<Me>()
  const [spaces, setSpaces] = useState<Space[]>([])
  const [notes, setNotes] = useState<Note[]>([])
  const [members, setMembers] = useState<Member[]>([])
  const [shared, setShared] = useState<SharedNote[]>([])
  const [modules, setModules] = useState<string[] | null>(null)
  const [denied, setDenied] = useState(false)
  const [tick, setTick] = useState(0)
  const [spaceDialog, setSpaceDialog] = useState(false)
  const [palette, setPalette] = useState(false)
  const fold = useFold(notes, path.match(/\/n\/([^/?]+)/)?.[1])

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
      loadShared(ws),
      cmd<{ settings: { modules?: string[] } }>(ws, 'workspace.get'),
    ]).then(
      ([s, n, m, sh, w]) => {
        setModules(w.settings.modules ?? null)
        setSpaces(s)
        setNotes([...n, ...sh.roots, ...sh.extra])
        setMembers(m)
        setShared(sh.roots)
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
    shared,
    members,
    modules,
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
      <FoldCtx.Provider value={fold}>
        <div className="flex h-screen">
          <nav
            aria-label="Modules"
            className="flex w-12 shrink-0 flex-col items-center gap-1 border-r border-line bg-side py-2"
          >
            {MODULES.filter((m) => moduleOn(modules, m.key)).map((m) => (
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
              href={`${base}/marketplace`}
              aria-label="Marketplace"
              title="Marketplace"
              aria-current={active('marketplace') ? 'page' : undefined}
              className="rounded-md p-2 text-muted hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:text-fg"
            >
              <Store size={18} />
            </Link>
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
                      <Link
                        key={w.id}
                        href={`/w/${w.id}`}
                        onClick={close}
                        role="menuitem"
                        className={menuItem}
                      >
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
              {shared.length > 0 && (
                <div className="mb-1" data-testid="shared-with-me">
                  <div className="px-2 py-1 text-[11px] font-semibold uppercase tracking-wide text-muted">
                    Shared with me
                  </div>
                  <ul>
                    {shared.map((n) => (
                      <li key={n.id}>
                        <Link
                          href={`${base}/n/${n.id}`}
                          className="flex items-center gap-1.5 rounded py-1 pr-2 pl-4 text-[13px] hover:bg-hover"
                        >
                          <FileText size={13} className="shrink-0 text-muted" />
                          <span className="truncate">{n.title || 'Untitled'}</span>
                        </Link>
                        <Tree spaceId={n.space_id} notes={notes} parent={n.id} depth={2} />
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {spaces.map((s) => {
                const open = fold.isOpen(s.id, true)
                return (
                  <div key={s.id} className="mb-1">
                    <div className="flex items-center rounded hover:bg-hover">
                      <FoldButton id={s.id} label={s.name} open={open} dflt />
                      <Link
                        href={`${base}/s/${s.id}`}
                        className="flex min-w-0 flex-1 items-center gap-1.5 py-1 pr-2 pl-0.5 text-[13px] font-medium"
                      >
                        <span className="rounded bg-hover px-1 text-[10px] font-bold text-muted">
                          {s.key}
                        </span>
                        <span className="truncate">{s.name}</span>
                      </Link>
                    </div>
                    {open && <Tree spaceId={s.id} notes={notes} parent={null} depth={1} />}
                  </div>
                )
              })}
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
              <NotificationBell ws={ws} />
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
      </FoldCtx.Provider>
    </Ctx.Provider>
  )
}
