'use client'
import {
  ChevronDown,
  ChevronRight,
  ClipboardCheck,
  FileText,
  Lock,
  MoreHorizontal,
  PanelLeft,
  Plus,
  Search,
  Settings,
  Sparkles,
  Store,
  Trash2,
  UsersRound,
} from 'lucide-react'
import Link from 'next/link'
import { usePathname, useRouter } from 'next/navigation'
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { auth, cmd, type Me, type Member, type Note, type SharedNote, type Space } from '@/lib/api'
import type { SpaceB1, TreeRow as TreeRowData } from '@/lib/docs'
import { MODULES, moduleOn } from '@/lib/modules'
import { prependWidget } from '@/lib/screen-config'
import { AskPanel } from './ai/ask-panel'
import { ChatNavLink } from './chat/nav'
import { TreeActionsProvider, useTreeActions } from './docs/page-actions'
import { MineSections } from './docs/page-header'
import { DropMarker, spaceRowId, TreeDnd, useDropZone, useRowDnd } from './docs/tree-dnd'
import { type TreeStore, useTreeStore } from './docs/tree-loader'
import { isShared, kidKey, sharedParent } from './docs/tree-model'
import { NotificationBell } from './notifications'
import { Palette } from './palette'
import { PersonHoverLayer } from './person'
import { SpaceDialog } from './space-dialog'
import { Avatar, btnPrimary } from './ui'

type Shell = {
  ws: string
  me: Me
  spaces: Space[]
  /** The page tree as loaded so far (levels load as nodes open; see docs/tree-loader). */
  tree: TreeStore
  shared: SharedNote[]
  members: Member[]
  /** Modules not uninstalled in the Marketplace (settings.hidden_modules); null = all shown. */
  modules: string[] | null
  /** settings.first_goal: the first thing the owner chose in setup; only orders the default Home. */
  goal: string | null
  /** settings.deal_stage_labels: display labels for the fixed deal stage keys. */
  dealLabels: Record<string, string> | null
  reload: () => void
  newPage: (opts?: { spaceId?: string; parentId?: string }) => Promise<void>
  openSpaceDialog: () => void
  togglePalette: () => void
  /** Open the Ask panel, optionally submitting a question. */
  openAsk: (q?: string) => void
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
function useFold(onPath: Set<string>): Fold {
  const [over, setOver] = useState<Record<string, boolean>>({})
  useEffect(() => {
    try {
      setOver(JSON.parse(localStorage.getItem(FOLD_KEY) ?? '{}'))
    } catch {}
  }, [])
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

/** Tree keyboard keys shared by the row links and the "Show more" buttons: arrows, Home and End over every row in the sidebar. */
const NAV = 'a[data-treelink],button[data-treemore]'
function navKeys(e: React.KeyboardEvent<HTMLElement>) {
  if (e.altKey || e.ctrlKey || e.metaKey) return false
  const links = [...(e.currentTarget.closest('aside')?.querySelectorAll<HTMLElement>(NAV) ?? [])]
  const i = links.indexOf(e.currentTarget)
  const go = (l?: HTMLElement | null) => l?.focus()
  if (e.key === 'ArrowDown') go(links[i + 1])
  else if (e.key === 'ArrowUp') go(links[i - 1])
  else if (e.key === 'Home') go(links[0])
  else if (e.key === 'End') go(links[links.length - 1])
  else return false
  return true
}

function Tree({
  spaceId,
  parent,
  depth,
  label,
}: {
  spaceId: string
  parent: string | null
  depth: number
  /** Set on the top list of a tree: it becomes the tree widget; nested lists are groups. */
  label?: string
}) {
  const { tree } = useShell()
  const { ensure } = tree
  const key = kidKey(spaceId, parent)
  // a level loads when it is first drawn, which is when its node opens
  useEffect(() => ensure(spaceId, parent), [ensure, spaceId, parent])
  const list = tree.kids.get(key) ?? []
  const lv = tree.levels.get(key)
  const partial = !!(lv?.next || lv?.prev)
  return (
    <ul role={label ? 'tree' : 'group'} aria-label={label} aria-busy={!lv}>
      {lv?.prev && <MoreRow spaceId={spaceId} parent={parent} dir="prev" depth={depth} />}
      {list.map((n, i) => (
        <TreeRow
          key={n.id}
          n={n}
          depth={depth}
          pos={lv?.prev ? undefined : i + 1}
          size={partial ? -1 : list.length}
          shared={isShared(parent)}
        />
      ))}
      {lv?.next && <MoreRow spaceId={spaceId} parent={parent} dir="next" depth={depth} />}
      {lv?.failed && (
        // biome-ignore lint/a11y/useFocusableInteractive: focus lives on the button inside
        <li role="treeitem" aria-level={depth} aria-label="Retry loading pages">
          <button
            type="button"
            data-treemore
            tabIndex={-1}
            onClick={() => ensure(spaceId, parent)}
            onKeyDown={(e) => navKeys(e) && e.preventDefault()}
            style={{ paddingLeft: depth * 12 + 8 }}
            className="w-full rounded py-1 pr-2 text-left text-[12px] text-bad hover:bg-hover"
          >
            Couldn't load pages. Retry
          </button>
        </li>
      )}
    </ul>
  )
}

/** The end of a level that has more rows ("Show more") or the start of a window that has earlier ones. */
function MoreRow({
  spaceId,
  parent,
  dir,
  depth,
}: {
  spaceId: string
  parent: string | null
  dir: 'next' | 'prev'
  depth: number
}) {
  const { tree } = useShell()
  const { announce } = useTreeActions()
  const [busy, setBusy] = useState(false)
  const text = dir === 'next' ? 'Show more' : 'Show earlier pages'
  async function go() {
    setBusy(true)
    const rows = await tree.more(spaceId, parent, dir)
    setBusy(false)
    if (!rows.length) return announce("Couldn't load more pages. Try again.")
    announce(`Loaded ${rows.length} ${rows.length === 1 ? 'page' : 'pages'}.`)
    // the button is gone when the level ended: put focus on the first new row so keyboard users keep their place
    const first = dir === 'next' ? rows[0] : rows[rows.length - 1]
    setTimeout(() => document.querySelector<HTMLElement>(`a[data-treelink="${first.id}"]`)?.focus(), 60)
  }
  return (
    // biome-ignore lint/a11y/useFocusableInteractive: focus lives on the button inside
    <li role="treeitem" aria-level={depth} aria-label={text}>
      <button
        type="button"
        data-treemore
        tabIndex={-1}
        disabled={busy}
        onClick={go}
        onKeyDown={(e) => navKeys(e) && e.preventDefault()}
        style={{ paddingLeft: depth * 12 + 8 }}
        className="w-full rounded py-1 pr-2 text-left text-[12px] text-muted hover:bg-hover hover:text-fg"
      >
        {busy ? 'Loading…' : text}
      </button>
    </li>
  )
}

function TreeRow({
  n,
  depth,
  pos,
  size,
  shared,
}: {
  n: TreeRowData
  depth: number
  pos?: number
  size: number
  shared: boolean
}) {
  const path = usePathname()
  const { ws } = useShell()
  const fold = useContext(FoldCtx)
  const a = useTreeActions()
  const { setNodeRef, listeners, isDragging, zone } = useRowDnd(n.id)
  const hasKids = n.has_children
  const open = hasKids && fold.isOpen(n.id, false)
  const current = path.endsWith(`/n/${n.id}`)
  const menuAt = (e: React.MouseEvent | React.KeyboardEvent, el: HTMLElement) => {
    const r = el.getBoundingClientRect()
    const m = e as React.MouseEvent
    const kb = !m.clientX && !m.clientY
    a.openMenu(n, { x: kb ? r.left + 24 : m.clientX, y: kb ? r.bottom : m.clientY }, el)
  }
  // ARIA tree keys on the row link; Alt+Up and Alt+Down reorder (docs-parity 7.9).
  const keys = (e: React.KeyboardEvent<HTMLAnchorElement>) => {
    if (e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) a.step(n, e.key === 'ArrowUp' ? -1 : 1)
    else if (navKeys(e)) {
      // handled
    } else if (e.altKey || e.ctrlKey || e.metaKey) return
    else if (e.key === 'ArrowRight' && hasKids) {
      if (!open) fold.toggle(n.id, false)
      else e.currentTarget.closest('li')?.querySelector<HTMLElement>('ul a[data-treelink]')?.focus() // first child, once loaded
    } else if (e.key === 'ArrowLeft') {
      if (open) fold.toggle(n.id, false)
      else
        e.currentTarget.closest('ul')?.closest('li')?.querySelector<HTMLElement>('a[data-treelink]')?.focus()
    } else if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) menuAt(e, e.currentTarget)
    else return
    e.preventDefault()
  }
  return (
    // biome-ignore lint/a11y/useFocusableInteractive: roving focus lives on the row link
    <li
      role="treeitem"
      aria-level={depth}
      aria-setsize={size}
      aria-posinset={pos}
      aria-expanded={hasKids ? open : undefined}
      aria-selected={current}
      aria-label={n.title || 'Untitled'}
    >
      {/* biome-ignore lint/a11y/noStaticElementInteractions: pointer drag and context menu; keys live on the link */}
      <div
        ref={setNodeRef}
        {...listeners}
        data-row-id={n.id}
        data-row-space={n.space_id}
        data-row-shared={shared || undefined}
        onContextMenu={(e) => {
          e.preventDefault()
          menuAt(e, e.currentTarget.querySelector<HTMLElement>('a') ?? e.currentTarget)
        }}
        style={{ paddingLeft: depth * 12 - 8 }}
        className={`group relative flex items-center rounded hover:bg-hover ${current ? 'bg-hover font-medium' : ''} ${isDragging ? 'opacity-40' : ''}`}
      >
        {hasKids ? (
          <FoldButton id={n.id} label={n.title || 'Untitled'} open={open} dflt={false} />
        ) : (
          <span className="w-4 shrink-0" />
        )}
        <Link
          href={`/w/${ws}/n/${n.id}`}
          draggable={false}
          data-treelink={n.id}
          tabIndex={a.tabStop === n.id ? 0 : -1}
          onFocus={() => a.setTabStop(n.id)}
          onKeyDown={keys}
          aria-current={current ? 'page' : undefined}
          className="flex min-w-0 flex-1 items-center gap-1.5 py-1 pr-2 pl-0.5 text-[13px]"
        >
          {n.icon ? (
            <span aria-hidden="true" className="w-[13px] shrink-0 text-center text-[12px] leading-none">
              {n.icon}
            </span>
          ) : (
            <FileText size={13} className="shrink-0 text-muted" />
          )}
          <span className="truncate">{n.title || 'Untitled'}</span>
          {n.restriction !== 'none' && (
            <Lock size={11} className="shrink-0 text-muted" aria-label="Restricted page" />
          )}
        </Link>
        <button
          type="button"
          aria-label={`Actions for ${n.title || 'Untitled'}`}
          aria-haspopup="menu"
          tabIndex={-1}
          onClick={(e) => menuAt(e, e.currentTarget)}
          className="mr-1 shrink-0 rounded p-0.5 text-muted opacity-0 hover:bg-line focus:opacity-100 group-hover:opacity-100 [@media(pointer:coarse)]:opacity-100"
        >
          <MoreHorizontal size={14} />
        </button>
        <DropMarker zone={zone} />
      </div>
      {open && <Tree spaceId={n.space_id} parent={n.id} depth={depth + 1} />}
    </li>
  )
}

function SpaceHead({ s, base, open }: { s: Space; base: string; open: boolean }) {
  const zone = useDropZone(spaceRowId(s.id))
  return (
    <div
      data-row-id={spaceRowId(s.id)}
      data-row-space={s.id}
      className="group/space relative flex items-center rounded hover:bg-hover"
    >
      <FoldButton id={s.id} label={s.name} open={open} dflt />
      <Link
        href={`${base}/s/${s.id}`}
        className="flex min-w-0 flex-1 items-center gap-1.5 py-1 pr-2 pl-0.5 text-[13px] font-medium"
      >
        <span className="rounded bg-hover px-1 text-[10px] font-bold text-muted">{s.key}</span>
        <span className="truncate">{s.name}</span>
      </Link>
      {(s as Partial<SpaceB1>).my_role === 'admin' && (
        <Link
          href={`${base}/s/${s.id}/settings`}
          aria-label={`${s.name} settings`}
          className="mr-1 rounded p-1 text-muted hover:bg-hover focus-visible:opacity-100 md:opacity-0 md:group-hover/space:opacity-100"
        >
          <Settings size={13} />
        </Link>
      )}
      <DropMarker zone={zone} />
    </div>
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
    // the path comes from what the tree loaded for the open page (its reveal), root first
    const chain: TreeRowData[] = []
    for (let n = s.tree.byId.get(seg[1]); n; n = n.parent_id ? s.tree.byId.get(n.parent_id) : undefined)
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

export function ShellView({
  ws,
  homeWidgets,
  children,
}: {
  ws: string
  homeWidgets?: boolean
  children: React.ReactNode
}) {
  const router = useRouter()
  const path = usePathname()
  const [me, setMe] = useState<Me>()
  const [spaces, setSpaces] = useState<Space[]>([])
  const [members, setMembers] = useState<Member[]>([])
  const [shared, setShared] = useState<SharedNote[]>([])
  const [modules, setModules] = useState<string[] | null>(null)
  const [dealLabels, setDealLabels] = useState<Record<string, string> | null>(null)
  const [goal, setGoal] = useState<string | null>(null)
  const [denied, setDenied] = useState(false)
  // Phones: the Spaces column is a drawer (closed on every navigation) so pages get the full width.
  const [spacesOpen, setSpacesOpen] = useState(false)
  // biome-ignore lint/correctness/useExhaustiveDependencies: close whenever the route changes
  useEffect(() => setSpacesOpen(false), [path])
  const [tick, setTick] = useState(0)
  const [spaceDialog, setSpaceDialog] = useState(false)
  const [palette, setPalette] = useState(false)
  const [ask, setAsk] = useState(false)
  const [askSeed, setAskSeed] = useState<{ q: string; n: number } | null>(null)
  const cur = path.match(/\/n\/([^/?]+)/)?.[1]
  const tree = useTreeStore(ws, !!me && !denied)
  // The open page and its loaded ancestors start unfolded.
  const onPath = useMemo(() => {
    const ids = new Set<string>()
    for (
      let n = cur ? tree.byId.get(cur) : undefined;
      n;
      n = n.parent_id ? tree.byId.get(n.parent_id) : undefined
    )
      ids.add(n.id)
    return ids
  }, [tree.byId, cur])
  const fold = useFold(onPath)
  // Opening a page that is not loaded yet reveals its readable path. One try per page: an archived page is never a row.
  const tried = useRef(new Set<string>())
  const { reveal } = tree
  useEffect(() => {
    if (!me || !cur || tree.byId.has(cur) || tried.current.has(`${ws}:${cur}`)) return
    tried.current.add(`${ws}:${cur}`)
    reveal(cur).catch(() => {})
  }, [me, ws, cur, tree.byId, reveal])

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
      cmd<Member[]>(ws, 'member.list'),
      cmd<SharedNote[]>(ws, 'note.shared_with_me'),
      cmd<{
        settings: {
          hidden_modules?: string[]
          first_goal?: string
          deal_stage_labels?: Record<string, string>
        }
      }>(ws, 'workspace.get'),
    ]).then(
      ([s, m, sh, w]) => {
        const off = w.settings.hidden_modules
        setModules(off?.length ? MODULES.map((m) => m.key).filter((k) => !off.includes(k)) : null)
        setDealLabels(w.settings.deal_stage_labels ?? null)
        setGoal(w.settings.first_goal ?? null)
        setSpaces(s)
        setMembers(m)
        setShared(sh)
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
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'j') {
        e.preventDefault()
        setAsk((a) => !a)
      }
    }
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [])

  const { refresh } = tree
  const reload = useCallback(() => {
    setTick((t) => t + 1)
    refresh()
  }, [refresh])

  const currentSpaceId = useMemo(() => {
    const seg = path.split('/').slice(3)
    if (seg[0] === 's') return seg[1]
    if (seg[0] === 'n') return tree.byId.get(seg[1])?.space_id
  }, [path, tree.byId])

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
    tree,
    shared,
    members,
    modules,
    goal,
    dealLabels,
    reload,
    newPage,
    openSpaceDialog: () => setSpaceDialog(true),
    togglePalette: () => setPalette((p) => !p),
    openAsk: (q) => {
      if (q) setAskSeed((s) => ({ q, n: (s?.n ?? 0) + 1 }))
      setAsk(true)
    },
  }
  const current = me.workspaces.find((w) => w.id === ws)
  const isAdmin = current?.role === 'owner' || current?.role === 'admin'
  const base = `/w/${ws}`
  const hrefFor = (key: string) =>
    key === 'docs' ? (spaces[0] ? `${base}/s/${spaces[0].id}` : base) : key ? `${base}/${key}` : base
  const active = (key: string) =>
    key === '' ? path === base : key === 'docs' ? /\/(s|n)\//.test(path) : path.startsWith(`${base}/${key}`)

  return (
    <Ctx.Provider value={shell}>
      <FoldCtx.Provider value={fold}>
        <TreeActionsProvider>
          <TreeDnd onOpen={(id) => !fold.isOpen(id, false) && fold.toggle(id, false)}>
            <div className="flex h-screen">
              <nav
                aria-label="Modules"
                className="flex w-12 shrink-0 flex-col items-center gap-1 border-r border-line bg-side py-2"
              >
                <button
                  type="button"
                  aria-label="Spaces"
                  aria-expanded={spacesOpen}
                  aria-controls="spaces-panel"
                  onClick={() => setSpacesOpen((o) => !o)}
                  className="grid h-11 w-11 place-items-center rounded-md text-muted hover:bg-hover hover:text-fg md:hidden"
                >
                  <PanelLeft size={18} aria-hidden />
                </button>
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
                <div className="mt-auto flex flex-col gap-1">
                  <ChatNavLink ws={ws} active={active('chat')} />
                </div>
                <Link
                  href={`${base}/people`}
                  aria-label="People"
                  title="People"
                  aria-current={active('people') ? 'page' : undefined}
                  className="rounded-md p-2 text-muted hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:text-fg"
                >
                  <UsersRound size={18} />
                </Link>
                <Link
                  href={`${base}/approvals`}
                  aria-label="Approvals"
                  title="Approvals"
                  aria-current={active('approvals') ? 'page' : undefined}
                  className="rounded-md p-2 text-muted hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:text-fg"
                >
                  <ClipboardCheck size={18} />
                </Link>
                <Link
                  href={`${base}/settings`}
                  aria-label="Settings"
                  title="Settings"
                  aria-current={active('settings') ? 'page' : undefined}
                  className="rounded-md p-2 text-muted hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:text-fg"
                >
                  <Settings size={18} />
                </Link>
              </nav>

              <aside
                id="spaces-panel"
                aria-label="Spaces"
                className={`${spacesOpen ? 'fixed inset-y-0 left-12 z-40 flex shadow-lg' : 'hidden'} w-60 shrink-0 flex-col border-r border-line bg-side md:static md:flex md:shadow-none`}
              >
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
                            if (name?.trim())
                              router.push(`/w/${(await auth.createWorkspace(name.trim())).id}`)
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
                  <Link
                    href={`${base}/tasks`}
                    aria-current={path === `${base}/tasks` ? 'page' : undefined}
                    className="mb-1 flex items-center gap-1.5 rounded px-2 py-1 text-[13px] hover:bg-hover aria-[current=page]:bg-hover"
                  >
                    <ClipboardCheck size={13} /> My tasks
                  </Link>
                  <MineSections ws={ws} />
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
                      {[...new Set(shared.map((n) => n.space_id))].map((sp, i) => (
                        <Tree
                          key={sp}
                          spaceId={sp}
                          parent={sharedParent(sp)}
                          depth={1}
                          label={i === 0 ? 'Shared with me' : `Shared with me (${i + 1})`}
                        />
                      ))}
                    </div>
                  )}
                  {spaces.map((s) => {
                    const open = fold.isOpen(s.id, true)
                    return (
                      <div key={s.id} className="mb-1">
                        <SpaceHead s={s} base={base} open={open} />
                        {open && <Tree spaceId={s.id} parent={null} depth={1} label={`${s.name} pages`} />}
                      </div>
                    )
                  })}
                  <Link
                    href={`${base}/trash`}
                    aria-current={path === `${base}/trash` ? 'page' : undefined}
                    className="mt-1 flex items-center gap-1.5 rounded px-2 py-1 text-[13px] text-muted hover:bg-hover aria-[current=page]:bg-hover aria-[current=page]:text-fg"
                  >
                    <Trash2 size={13} /> Trash
                  </Link>
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
                  <button
                    type="button"
                    onClick={() => setAsk((a) => !a)}
                    aria-label="Ask your business"
                    aria-keyshortcuts="Meta+J Control+J"
                    className="flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1 text-[13px] hover:bg-hover"
                  >
                    <Sparkles size={14} /> Ask
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
                        {isAdmin && homeWidgets && (
                          <button
                            type="button"
                            role="menuitem"
                            className={menuItem}
                            onClick={async () => {
                              close()
                              // best effort: Home still opens if the layout cannot be saved
                              await prependWidget(ws, 'home', {
                                id: 'setup',
                                type: 'setup',
                                size: 'l',
                              }).catch(() => {})
                              router.push(base)
                            }}
                          >
                            Setup guide
                          </button>
                        )}
                        <Link href={`${base}/settings`} onClick={close} role="menuitem" className={menuItem}>
                          Settings
                        </Link>
                        <Link href="/account/security" onClick={close} role="menuitem" className={menuItem}>
                          Account security
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
          </TreeDnd>
        </TreeActionsProvider>
        <SpaceDialog open={spaceDialog} onClose={() => setSpaceDialog(false)} />
        <AskPanel open={ask} onClose={() => setAsk(false)} seed={askSeed} />
        <Palette open={palette} onClose={() => setPalette(false)} />
        <PersonHoverLayer />
      </FoldCtx.Provider>
    </Ctx.Provider>
  )
}
