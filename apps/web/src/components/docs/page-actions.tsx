'use client'
import { MoreHorizontal } from 'lucide-react'
import { usePathname, useRouter } from 'next/navigation'
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { Note } from '@/lib/api'
import { noteDelete, noteDuplicate, noteMove, noteReorder, TREE_EVENT } from '@/lib/docs'
import { useShell } from '../shell'
import { btn, ErrorText, Modal } from '../ui'
import { MoveDialog } from './move-dialog'
import { buildKids, kidKey, liveParent, subtreeIds } from './tree-model'

/** Shown verbatim when a delete could not fold unsaved edits; the dialog offers a retry (docs-parity 7.10 item 4). */
export const SAVE_FAILED = "Couldn't save the latest edits. Try again."

type Actions = {
  kids: Map<string, Note[]>
  byId: Map<string, Note>
  /** The one tree row in the tab order (roving tabindex). */
  tabStop: string | null
  setTabStop: (id: string) => void
  openMenu: (n: Note, at: { x: number; y: number }, back?: HTMLElement | null) => void
  /** Reorder among siblings by one (Alt+Up, Alt+Down, menu). */
  step: (n: Note, dir: -1 | 1) => Promise<void>
  /** Move to a parent (null = space root) before a sibling (null = last); throws the API error. */
  place: (n: Note, parent: string | null, before: string | null) => Promise<void>
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
  const { ws, notes, spaces, reload } = useShell()
  const router = useRouter()
  const path = usePathname()
  const kids = useMemo(() => buildKids(notes), [notes])
  const byId = useMemo(() => new Map(notes.map((n) => [n.id, n])), [notes])
  const cur = path.match(/\/n\/([^/?]+)/)?.[1]
  const first = spaces.map((s) => kids.get(kidKey(s.id, null))?.[0]?.id).find(Boolean) ?? null
  const [msg, setMsg] = useState('')
  const [tab, setTab] = useState<string | null>(null)
  const [menu, setMenu] = useState<{ n: Note; x: number; y: number } | null>(null)
  const [dlg, setDlg] = useState<{ kind: 'move' | 'delete'; n: Note } | null>(null)
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
    window.addEventListener(TREE_EVENT, reload)
    return () => window.removeEventListener(TREE_EVENT, reload)
  }, [reload])
  // biome-ignore lint/correctness/useExhaustiveDependencies: restore focus after the tree re-rendered
  useEffect(() => {
    const id = refocus.current
    if (!id) return
    refocus.current = null
    document.querySelector<HTMLElement>(`a[data-treelink="${id}"]`)?.focus()
  }, [kids])

  const place = useCallback(
    async (n: Note, parent: string | null, before: string | null) => {
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
    async (n: Note, dir: -1 | 1) => {
      const sibs = kids.get(kidKey(n.space_id, liveParent(n, byId))) ?? []
      const i = sibs.findIndex((s) => s.id === n.id)
      const j = i + dir
      if (i < 0 || j < 0 || j >= sibs.length)
        return announce(`${name(n)} is already ${dir < 0 ? 'first' : 'last'}.`)
      try {
        await noteReorder(ws, n.id, dir < 0 ? sibs[i - 1].id : (sibs[i + 2]?.id ?? null))
        announce(`Moved ${name(n)} ${dir < 0 ? 'up' : 'down'}. Position ${j + 1} of ${sibs.length}.`)
        refocus.current = n.id
        tell()
      } catch (e) {
        announce((e as Error).message)
      }
    },
    [kids, byId, ws, announce],
  )
  const duplicate = async (n: Note) => {
    try {
      const r = await noteDuplicate(ws, n.id)
      announce(`Created ${r.title}.`)
      tell()
      router.push(`/w/${ws}/n/${r.id}`)
    } catch (e) {
      announce((e as Error).message)
    }
  }
  const copyLink = async (n: Note) => {
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
          {menu.n.types.length === 0 && (
            <button type="button" role="menuitem" className={item} onClick={run(() => duplicate(menu.n))}>
              Duplicate
            </button>
          )}
          <button type="button" role="menuitem" className={item} onClick={run(() => copyLink(menu.n))}>
            Copy link
          </button>
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
          count={subtreeIds(kids, dlg.n.id).size}
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
  count,
  onClose,
  onDone,
}: {
  note: Note
  count: number
  onClose: () => void
  onDone: (count: number, title: string) => void
}) {
  const { ws } = useShell()
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const retry = err === SAVE_FAILED
  async function go() {
    setBusy(true)
    setErr(undefined)
    try {
      const r = await noteDelete(ws, note.id)
      onDone(r.count ?? count, name(note))
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }
  return (
    <Modal open onClose={onClose} title="Delete page">
      <p className="mb-3 text-[13px]">
        {count === 1
          ? `Move "${name(note)}" to Trash?`
          : `Move "${name(note)}" and its ${count - 1} ${count === 2 ? 'sub-page' : 'sub-pages'} to Trash? That is ${count} pages in all.`}{' '}
        You can restore {count === 1 ? 'it' : 'them'} from Trash.
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
export function PageMenuButton({ note }: { note: Note }) {
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
