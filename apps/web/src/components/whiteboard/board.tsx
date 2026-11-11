'use client'
import { Download, IdCard, Palette, Printer } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  createShapeId,
  type Editor,
  exportAs,
  getSnapshot,
  InstancePresenceRecordType,
  loadSnapshot,
  type TLRecord,
  type TLShapeId,
  type TLStoreSnapshot,
  Tldraw,
  toRichText,
} from 'tldraw'
import { WebsocketProvider } from 'y-websocket'
import * as Y from 'yjs'
import 'tldraw/tldraw.css'
import { useShell } from '@/components/shell'
import { InsertField } from '@/components/templates/fields'
import { SaveAsTemplate } from '@/components/templates/save-template'
import { btn, ErrorText, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { type BrandKit, BrandPanel } from './brand'
import { ItemEmbedUtil } from './item-embed'

const COLLAB_URL = process.env.NEXT_PUBLIC_COLLAB_URL ?? 'ws://localhost:4000'
const SYNC_TIMEOUT_MS = 2000
const LICENSE = process.env.NEXT_PUBLIC_TLDRAW_LICENSE_KEY
const shapeUtils = [ItemEmbedUtil]

const PRESETS = [
  { label: 'Instagram post 1080x1080', name: 'Instagram post', w: 1080, h: 1080 },
  { label: 'A4 595x842', name: 'A4', w: 595, h: 842 },
  { label: 'Presentation 1920x1080', name: 'Presentation', w: 1920, h: 1080 },
  { label: 'Invoice header 800x200', name: 'Invoice header', w: 800, h: 200 },
]

const FENCE = /```tldraw\n([\s\S]*?)\n```/
type Mode = 'connecting' | 'collab' | 'solo'
type ItemRow = { id: string; key: string; title: string; status: string }

/** Mirror document-scope store records into a Y.Map, and remote map changes (and awareness cursors) back. */
function bindYjs(editor: Editor, doc: Y.Doc, provider: WebsocketProvider, userName: string) {
  const map = doc.getMap<TLRecord>('tldraw')
  const { store } = editor
  if (map.size > 0) store.mergeRemoteChanges(() => store.put([...map.values()]))
  else
    doc.transact(() => {
      for (const r of store.allRecords()) if (store.scopedTypes.document.has(r.typeName)) map.set(r.id, r)
    }, 'local')

  const stopStore = store.listen(
    ({ changes }) =>
      doc.transact(() => {
        for (const r of Object.values(changes.added)) map.set(r.id, r)
        for (const [, to] of Object.values(changes.updated)) map.set(to.id, to)
        for (const r of Object.values(changes.removed)) map.delete(r.id)
      }, 'local'),
    { source: 'user', scope: 'document' },
  )
  const onMap = (ev: Y.YMapEvent<TLRecord>, tr: Y.Transaction) => {
    if (tr.origin === 'local') return
    store.mergeRemoteChanges(() => {
      for (const [id, c] of ev.changes.keys) {
        if (c.action === 'delete') store.remove([id as TLRecord['id']])
        else store.put([map.get(id) as TLRecord])
      }
    })
  }
  map.observe(onMap)

  const aw = provider.awareness
  const color = `hsl(${doc.clientID % 360} 70% 50%)`
  aw.setLocalStateField('user', { name: userName, color })
  const move = () => aw.setLocalStateField('cursor', editor.inputs.getCurrentPagePoint().toJson())
  const el = editor.getContainer()
  el.addEventListener('pointermove', move)
  const shown = new Set<TLRecord['id']>()
  const onAw = () => {
    const recs: TLRecord[] = []
    for (const [cid, st] of aw.getStates()) {
      if (cid === doc.clientID || !st.user) continue
      const cur = st.cursor as { x: number; y: number } | undefined
      recs.push(
        InstancePresenceRecordType.create({
          id: InstancePresenceRecordType.createId(String(cid)),
          currentPageId: editor.getCurrentPageId(),
          userId: String(cid) as never,
          userName: st.user.name,
          color: st.user.color,
          cursor: cur ? { x: cur.x, y: cur.y, type: 'default', rotation: 0 } : null,
          lastActivityTimestamp: Date.now(),
        }),
      )
    }
    store.mergeRemoteChanges(() => {
      const gone = [...shown].filter((id) => !recs.some((r) => r.id === id))
      if (gone.length) store.remove(gone)
      store.put(recs)
      shown.clear()
      for (const r of recs) shown.add(r.id)
    })
  }
  aw.on('change', onAw)
  return () => {
    stopStore()
    map.unobserve(onMap)
    aw.off('change', onAw)
    el.removeEventListener('pointermove', move)
  }
}

export function Board({
  id,
  title,
  contentMd,
  design,
}: {
  id: string
  title: string
  contentMd: string
  design: boolean
}) {
  const { ws, me } = useShell()
  const [mode, setMode] = useState<Mode>('connecting')
  const doc = useRef<Y.Doc>(null)
  const provider = useRef<WebsocketProvider>(null)
  const [editor, setEditor] = useState<Editor>()
  const [panel, setPanel] = useState<'' | 'item' | 'brand'>('')
  const [err, setErr] = useState<string>()

  useEffect(() => {
    const d = new Y.Doc()
    const p = new WebsocketProvider(`${COLLAB_URL}/collab`, `${ws}/${id}`, d)
    doc.current = d
    provider.current = p
    let decided = false
    const decide = (m: Mode) => {
      if (decided) return
      decided = true
      if (m === 'solo') p.destroy()
      setMode(m)
    }
    p.once('sync', () => decide('collab'))
    const t = setTimeout(() => decide('solo'), SYNC_TIMEOUT_MS)
    return () => {
      clearTimeout(t)
      p.destroy()
      d.destroy()
    }
  }, [ws, id])

  // Sync (collab) or debounced content_md snapshot (solo) once the editor mounts.
  useEffect(() => {
    if (!editor) return
    if (mode === 'collab' && doc.current && provider.current)
      return bindYjs(editor, doc.current, provider.current, me.user.name)
    let t: ReturnType<typeof setTimeout>
    const stop = editor.store.listen(
      () => {
        clearTimeout(t)
        t = setTimeout(() => {
          const md = `\`\`\`tldraw\n${JSON.stringify(getSnapshot(editor.store).document)}\n\`\`\``
          cmd(ws, 'note.update', { id, content_md: md }).catch((e) => setErr((e as Error).message))
        }, 800)
      },
      { source: 'user', scope: 'document' },
    )
    return () => {
      clearTimeout(t)
      stop()
    }
  }, [editor, mode, ws, id, me.user.name])

  const mount = useCallback(
    (e: Editor) => {
      e.user.updateUserPreferences({ colorScheme: 'system' })
      // Solo, or collab with an empty room: a board rendered from a template has its snapshot only in content_md.
      const m = mode === 'solo' || doc.current?.getMap('tldraw').size === 0 ? FENCE.exec(contentMd) : null
      if (m) {
        try {
          loadSnapshot(e.store, { document: JSON.parse(m[1]) as TLStoreSnapshot } as never)
        } catch {
          setErr('Could not read the saved board snapshot.')
        }
      }
      setEditor(e)
    },
    [mode, contentMd],
  )

  const exportIds = (e: Editor): TLShapeId[] => {
    const sel = e.getSelectedShapeIds()
    return sel.length ? sel : [...e.getCurrentPageShapeIds()]
  }
  async function doExport(format: 'png' | 'svg') {
    if (!editor) return
    const ids = exportIds(editor)
    if (ids.length === 0) return setErr('Nothing to export yet.')
    setErr(undefined)
    await exportAs(editor, ids, { format, name: title || 'board' })
  }
  async function print() {
    if (!editor) return
    const ids = exportIds(editor)
    if (ids.length === 0) return setErr('Nothing to export yet.')
    const { blob } = await editor.toImage(ids, { format: 'png' })
    const url = URL.createObjectURL(blob)
    const w = window.open('', '_blank')
    if (!w) return setErr('Allow pop-ups to print / save as PDF.')
    const img = w.document.createElement('img')
    img.style.maxWidth = '100%'
    img.onload = () => w.print()
    img.src = url
    w.document.body.append(img)
  }
  function insertField(text: string) {
    if (!editor) return
    const c = editor.getViewportPageBounds().center
    editor.createShape({
      type: 'text',
      x: c.x,
      y: c.y,
      props: { richText: toRichText(text), autoSize: true },
    })
  }
  function addFrame(p: (typeof PRESETS)[number]) {
    if (!editor) return
    const c = editor.getViewportPageBounds().center
    editor.createShape({
      id: createShapeId(),
      type: 'frame',
      x: c.x - p.w / 2,
      y: c.y - p.h / 2,
      props: { w: p.w, h: p.h, name: p.name },
    })
    editor.zoomToFit()
  }

  return (
    <div className="relative flex h-full flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-1.5">
        <h1 className="mr-2 truncate text-[14px] font-semibold">{title || 'Untitled'}</h1>
        <span className="text-[12px] text-muted">
          {mode === 'collab' ? 'Live' : mode === 'solo' ? 'Offline: saving to the page' : 'Connecting...'}
        </span>
        <ErrorText error={err} />
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <InsertField ws={ws} onInsert={insertField} />
          <SaveAsTemplate ws={ws} noteId={id} title={title} />
          {design && (
            <select
              aria-label="Add frame"
              className={`${inputCls} w-auto`}
              value=""
              onChange={(e) => {
                const p = PRESETS.find((x) => x.name === e.target.value)
                if (p) addFrame(p)
              }}
            >
              <option value="">Add frame...</option>
              {PRESETS.map((p) => (
                <option key={p.name} value={p.name}>
                  {p.label}
                </option>
              ))}
            </select>
          )}
          <button
            type="button"
            className={btn}
            aria-pressed={panel === 'item'}
            onClick={() => setPanel(panel === 'item' ? '' : 'item')}
          >
            <IdCard size={14} /> Item
          </button>
          {design && (
            <button
              type="button"
              className={btn}
              aria-pressed={panel === 'brand'}
              onClick={() => setPanel(panel === 'brand' ? '' : 'brand')}
            >
              <Palette size={14} /> Brand kit
            </button>
          )}
          <button type="button" className={btn} onClick={() => doExport('png')}>
            <Download size={14} /> PNG
          </button>
          <button type="button" className={btn} onClick={() => doExport('svg')}>
            <Download size={14} /> SVG
          </button>
          <button type="button" className={btn} onClick={print}>
            <Printer size={14} /> PDF
          </button>
        </div>
      </div>
      <div className="relative min-h-0 flex-1">
        {mode === 'connecting' ? (
          <p className="p-6 text-muted">Loading board...</p>
        ) : (
          <div className="absolute inset-0" data-testid="board">
            <Tldraw shapeUtils={shapeUtils} licenseKey={LICENSE} onMount={mount} />
          </div>
        )}
        {editor && panel === 'item' && <ItemPicker ws={ws} editor={editor} onDone={() => setPanel('')} />}
        {editor && panel === 'brand' && <Brand ws={ws} editor={editor} />}
      </div>
    </div>
  )
}

function Brand({ ws, editor }: { ws: string; editor: Editor }) {
  const { me } = useShell()
  const { data, error } = useCmd<{ settings: { brand_kit?: Partial<BrandKit> } }>(ws, 'workspace.get')
  const [saved, setSaved] = useState<BrandKit>()
  if (error) return <ErrorText error={error} />
  if (!data) return null
  const k = saved ?? { colors: [], fonts: [], logo_url: '', ...data.settings.brand_kit }
  const role = me.workspaces.find((w) => w.id === ws)?.role
  return (
    <BrandPanel
      ws={ws}
      editor={editor}
      kit={k}
      admin={role === 'admin' || role === 'owner'}
      onSaved={setSaved}
    />
  )
}

function ItemPicker({ ws, editor, onDone }: { ws: string; editor: Editor; onDone: () => void }) {
  const [q, setQ] = useState('')
  const [rows, setRows] = useState<ItemRow[]>()
  const [err, setErr] = useState<string>()

  async function search(e: React.FormEvent) {
    e.preventDefault()
    try {
      const key = q
        .trim()
        .toUpperCase()
        .replace(/[^A-Z0-9-]/g, '')
      setRows(await cmd<ItemRow[]>(ws, 'item.list', { cnql: `key = "${key}"` }))
      setErr(undefined)
    } catch (x) {
      setErr((x as Error).message)
    }
  }
  function add(r: ItemRow) {
    const c = editor.getViewportPageBounds().center
    editor.createShape({
      type: 'item-embed',
      x: c.x - 130,
      y: c.y - 42,
      props: { itemKey: r.key, title: r.title, status: r.status, href: `/w/${ws}/n/${r.id}` },
    })
    onDone()
  }

  return (
    <div
      role="dialog"
      aria-label="Embed item"
      className="absolute left-3 top-3 z-[400] w-72 space-y-2 rounded-lg border border-line bg-canvas p-3 shadow-lg"
    >
      <form onSubmit={search} className="flex gap-2">
        <input
          aria-label="Item key"
          className={inputCls}
          placeholder="ENG-1"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <button type="submit" className={btn}>
          Find
        </button>
      </form>
      <ErrorText error={err} />
      {rows?.length === 0 && <p className="text-[13px] text-muted">No item with that key.</p>}
      {rows?.map((r) => (
        <button
          key={r.id}
          type="button"
          onClick={() => add(r)}
          className="block w-full rounded px-2 py-1 text-left text-[13px] hover:bg-hover"
        >
          <b>{r.key}</b> {r.title}
        </button>
      ))}
    </div>
  )
}
