'use client'
import { MoreHorizontal } from 'lucide-react'
import { usePathname, useRouter } from 'next/navigation'
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import {
  noteDelete,
  noteDuplicate,
  noteMove,
  noteReorder,
  TREE_EVENT,
  type TreeRow,
  treeLevel,
} from '@/lib/docs'
import { useShell } from '../shell'
import { btn, ErrorText, Modal } from '../ui'
import { MoveDialog } from './move-dialog'
import { countSubtree } from './tree-loader'
import { kidKey, liveParent, parentOfKey, subtreeIds } from './tree-model'

/** Asks the open page's editor to show its find bar. */
export const FIND_EVENT = 'cnote:find'

/** What the page menu needs of a page: a tree row, or the open page's note. */
export type PageRef = {
  id: string
  space_id: string
  parent_id: string | null
  title: string
  types?: string[]
}

/** Shown verbatim when a delete could not fold unsaved edits; the dialog offers a retry (docs-parity 7.10 item 4). */
export const SAVE_FAILED = "Couldn't save the latest edits. Try again."

type Actions = {
  kids: Map<string, TreeRow[]>
  byId: Map<string, TreeRow>
  /** The one tree row in the tab order (roving tabindex). */
  tabStop: string | null
  setTabStop: (id: string) => void
  openMenu: (n: PageRef, at: { x: number; y: number }, back?: HTMLElement | null) => void
  /** Reorder among siblings by one (Alt+Up, Alt+Down, menu). */
  step: (n: PageRef, dir: -1 | 1) => Promise<void>
  /** Move to a parent (null = space root) before a sibling (null = last); throws the API error. */
  place: (n: PageRef, parent: string | null, before: string | null) => Promise<void>
  announce: (msg: string) => void
}
const Ctx = createContext<Actions | null>(null)
export const useTreeActions = () => {
  const c = useContext(Ctx)
  if (!c) throw new Error('outside tree actions')
  return c
}
const tell = () => window.dispatchEvent(new Event(TREE_EVENT))
const name = (n: { title: string }) => n.title || 'Untitled'
const item =
  'flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover focus:bg-hover focus:outline-none'

/** Move, delete and duplicate for the tree rows and the page header, with one menu, two dialogs and one announcer. */
export function TreeActionsProvider({ children }: { children: React.ReactNode }) {
  const { ws, tree, spaces } = useShell()
  const { kids, byId, refresh } = tree
  const router = useRouter()
  const path = usePathname()
  const cur = path.match(/\/n\/([^/?]+)/)?.[1]
  const first = spaces.map((s) => kids.get(kidKey(s.id, null))?.[0]?.id).find(Boolean) ?? null
  const [msg, setMsg] = useState('')
  const [tab, setTab] = useState<string | null>(null)
  const [menu, setMenu] = useState<{ n: PageRef; x: number; y: number } | null>(null)
  const [dlg, setDlg] = useState<{ kind: 'move' | 'delete'; n: PageRef } | null>(null)
  const stop = tab && byId.has(tab) ? tab : cur && byId.has(cur) ? cur : first
  const back = useRef<HTMLElement | null>(null)
  const refocus = useRef<string | null>(null)
  const announce = useCallback((m: string) => {
    setMsg('')
    setTimeout(() => setMsg(m), 20) // a repeated text must still be announced
  }, [])
  useEffect(() => {
    if (!msg) return
    const t = setTimeout(() => setMsg(''), 6000)
    return () => clearTimeout(t)
  }, [msg])
  useEffect(() => {
    window.addEventListener(TREE_EVENT, refresh)
    return () => window.removeEventListener(TREE_EVENT, refresh)
  }, [refresh])
  // biome-ignore lint/correctness/useExhaustiveDependencies: restore focus after the tree re-rendered
  useEffect(() => {
    const id = refocus.current
    if (!id) return
    refocus.current = null
    document.querySelector<HTMLElement>(`a[data-treelink="${id}"]`)?.focus()
  }, [kids])

  const place = useCallback(
    async (n: PageRef, parent: string | null, before: string | null) => {
      if (parent === (n.parent_id ?? null)) await noteReorder(ws, n.id, before)
      else await noteMove(ws, n.id, parent, before)
      const to = parent
        ? name(byId.get(parent) ?? { title: '' })
        : (spaces.find((s) => s.id === n.space_id)?.name ?? 'the space')
      announce(`Moved ${name(n)} ${parent ? `into ${to}` : `to the top level of ${to}`}.`)
      refocus.current = n.id
      tell()
    },
    [ws, byId, spaces, announce],
  )
  const step = useCallback(
    async (n: PageRef, dir: -1 | 1) => {
      const key = tree.keyOf.get(n.id) ?? kidKey(n.space_id, liveParent(n, byId))
      const parent = parentOfKey(key)
      // A level can be longer than what is loaded: ask for the neighbours past the loaded rows before deciding.
      let sibs = kids.get(key) ?? []
      let i = sibs.findIndex((s) => s.id === n.id)
      if (i < 0) return announce(`${name(n)} is not in the tree yet.`)
      if (dir > 0 && i + 2 >= sibs.length)
        sibs = [...sibs, ...(await tree.peek(n.space_id, parent, 'next', i + 3 - sibs.length))]
      if (dir < 0 && i === 0) {
        const before = await tree.peek(n.space_id, parent, 'prev', 1)
        sibs = [...before, ...sibs]
        i = before.length
      }
      const j = i + dir
      if (j < 0 || j >= sibs.length) return announce(`${name(n)} is already ${dir < 0 ? 'first' : 'last'}.`)
      try {
        await noteReorder(ws, n.id, dir < 0 ? sibs[i - 1].id : (sibs[i + 2]?.id ?? null))
        announce(
          `Moved ${name(n)} ${dir < 0 ? 'up' : 'down'}.${tree.levels.get(key)?.next || tree.levels.get(key)?.prev ? '' : ` Position ${j + 1} of ${sibs.length}.`}`,
        )
        refocus.current = n.id
        tell()
      } catch (e) {
        announce((e as Error).message)
      }
    },
    [kids, byId, tree, ws, announce],
  )
  const duplicate = async (n: PageRef) => {
    try {
      const r = await noteDuplicate(ws, n.id)
      announce(`Created ${r.title}.`)
      tell()
      router.push(`/w/${ws}/n/${r.id}`)
    } catch (e) {
      announce((e as Error).message)
    }
  }
  const copyLink = async (n: PageRef) => {
    try {
      await navigator.clipboard.writeText(`${location.origin}/w/${ws}/n/${n.id}`)
      announce('Link copied.')
    } catch {
      announce("Couldn't copy the link.")
    }
  }

  // Menu: focus the first item, arrows move, Escape and outside press close and return focus.
  const menuRef = useRef<HTMLDivElement>(null)
  const closeMenu = useCallback(() => {
    setMenu(null)
    back.current?.focus()
  }, [])
  useEffect(() => {
    if (!menu) return
    menuRef.current?.querySelector<HTMLElement>('[role=menuitem]')?.focus()
    const out = (e: MouseEvent) => !menuRef.current?.contains(e.target as Node) && setMenu(null)
    document.addEventListener('mousedown', out)
    return () => document.removeEventListener('mousedown', out)
  }, [menu])
  const menuKeys = (e: React.KeyboardEvent) => {
    const items = [...(menuRef.current?.querySelectorAll<HTMLElement>('[role=menuitem]') ?? [])]
    const i = items.indexOf(document.activeElement as HTMLElement)
    if (e.key === 'Escape') closeMenu()
    else if (e.key === 'ArrowDown') items[(i + 1) % items.length]?.focus()
    else if (e.key === 'ArrowUp') items[(i - 1 + items.length) % items.length]?.focus()
    else if (e.key === 'Tab') setMenu(null)
    else return
    e.preventDefault()
  }

  const value: Actions = {
    kids,
    byId,
    tabStop: stop,
    setTabStop: setTab,
    openMenu: (n, at, el) => {
      back.current = el ?? null
      setMenu({ n, ...at })
    },
    step,
    place,
    announce,
  }
  const run = (fn: () => void) => () => {
    setMenu(null)
    fn()
  }
  return (
    <Ctx.Provider value={value}>
      {children}
      {menu && (
        <div
          ref={menuRef}
          role="menu"
          aria-label={`Actions for ${name(menu.n)}`}
          onKeyDown={menuKeys}
          style={{
            left: Math.max(4, Math.min(menu.x, window.innerWidth - 200)),
            top: Math.max(4, Math.min(menu.y, window.innerHeight - 260)),
          }}
          className="fixed z-50 w-48 rounded-md border border-line bg-canvas p-1 shadow-lg"
        >
          <button
            type="button"
            role="menuitem"
            className={item}
            onClick={run(() => setDlg({ kind: 'move', n: menu.n }))}
          >
            Move…
          </button>
          <button type="button" role="menuitem" className={item} onClick={run(() => step(menu.n, -1))}>
            Move up
          </button>
          <button type="button" role="menuitem" className={item} onClick={run(() => step(menu.n, 1))}>
            Move down
          </button>
          {!menu.n.types?.length && (
            <button type="button" role="menuitem" className={item} onClick={run(() => duplicate(menu.n))}>
              Duplicate
            </button>
          )}
          <button type="button" role="menuitem" className={item} onClick={run(() => copyLink(menu.n))}>
            Copy link
          </button>
          {path.endsWith(`/n/${menu.n.id}`) && (
            <button
              type="button"
              role="menuitem"
              className={item}
              onClick={run(() => window.dispatchEvent(new Event(FIND_EVENT)))}
            >
              Find and replace
            </button>
          )}
          <button
            type="button"
            role="menuitem"
            className={`${item} text-bad`}
            onClick={run(() => setDlg({ kind: 'delete', n: menu.n }))}
          >
            Delete
          </button>
        </div>
      )}
      {dlg?.kind === 'move' && <MoveDialog note={dlg.n} onClose={() => setDlg(null)} />}
      {dlg?.kind === 'delete' && (
        <DeleteDialog
          note={dlg.n}
          hasKids={byId.get(dlg.n.id)?.has_children ?? true}
          onClose={() => setDlg(null)}
          onDone={(count, title) => {
            setDlg(null)
            announce(`Moved ${title} to Trash (${count} ${count === 1 ? 'page' : 'pages'}).`)
            tell()
            const gone = subtreeIds(kids, dlg.n.id)
            const open = path.match(/\/n\/([^/?]+)/)?.[1]
            if (open && gone.has(open)) router.push(`/w/${ws}/s/${dlg.n.space_id}`)
          }}
        />
      )}
      {/* Visible and announced: results of moves, duplicates and refusals. */}
      <div
        role="status"
        aria-live="polite"
        className={
          msg
            ? 'fixed bottom-4 left-16 z-50 max-w-sm rounded-md bg-fg px-3 py-2 text-[13px] text-canvas shadow-lg'
            : 'sr-only'
        }
      >
        {msg}
      </div>
    </Ctx.Provider>
  )
}

function DeleteDialog({
  note,
  hasKids,
  onClose,
  onDone,
}: {
  note: PageRef
  hasKids: boolean
  onClose: () => void
  onDone: (count: number, title: string) => void
}) {
  const { ws } = useShell()
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [count, setCount] = useState<{ total: number; exact: boolean }>()
  useEffect(() => {
    if (!hasKids) return setCount({ total: 1, exact: true })
    countSubtree(ws, note.space_id, note.id)
      .then(setCount)
      .catch(() => setCount({ total: 2, exact: false }))
  }, [ws, note.id, note.space_id, hasKids])
  const retry = err === SAVE_FAILED
  async function go() {
    setBusy(true)
    setErr(undefined)
    try {
      const r = await noteDelete(ws, note.id)
      onDone(r.count ?? count?.total ?? 1, name(note))
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }
  const one = !hasKids
  return (
    <Modal open onClose={onClose} title="Delete page">
      <p className="mb-3 text-[13px]">
        {one
          ? `Move "${name(note)}" to Trash?`
          : count?.exact
            ? `Move "${name(note)}" and its ${count.total - 1} ${count.total === 2 ? 'sub-page' : 'sub-pages'} to Trash? That is ${count.total} pages in all.`
            : `Move "${name(note)}" and all its sub-pages to Trash?`}{' '}
        You can restore {one ? 'it' : 'them'} from Trash.
      </p>
      <ErrorText error={err} />
      <div className="mt-3 flex justify-end gap-2">
        <button type="button" className={btn} onClick={onClose}>
          Cancel
        </button>
        <button type="button" className={`${btn} text-bad`} disabled={busy} onClick={go}>
          {retry ? 'Retry' : 'Delete'}
        </button>
      </div>
    </Modal>
  )
}

/** The page header's "Page actions" button: opens the same menu as a tree row. */
export function PageMenuButton({ note }: { note: PageRef }) {
  const { openMenu } = useTreeActions()
  return (
    <button
      type="button"
      aria-label="Page actions"
      aria-haspopup="menu"
      className={btn}
      onClick={(e) => {
        const r = e.currentTarget.getBoundingClientRect()
        openMenu(note, { x: r.right - 192, y: r.bottom + 4 }, e.currentTarget)
      }}
    >
      <MoreHorizontal size={14} />
    </button>
  )
}
