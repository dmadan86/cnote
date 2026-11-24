'use client'
import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Check,
  Clock,
  Copy,
  Globe,
  HelpCircle,
  ImagePlus,
  Loader2,
  Mail,
  MessageSquare,
  Newspaper,
  Package,
  Redo2,
  Replace,
  Send,
  Settings,
  Square,
  Trash2,
  Type,
  Undo2,
  X,
} from 'lucide-react'
import dynamic from 'next/dynamic'
import Link from 'next/link'
import { useCallback, useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, EmptyState, ErrorText, Modal, Pill } from '@/components/ui'
import { type CmdFailure, cmd } from '@/lib/api'
import {
  blockDefs,
  blockLabel,
  blockType,
  type CatalogView,
  catalogApi,
  catalogChanges,
  catalogueView,
  dataOfPage,
  type EditOp,
  previewSnapshot,
  type Site,
  type SiteData,
  type SiteDomainMeta,
  type SiteMeta,
  type SiteVersion,
  siteApi,
  type Turn,
  viewOfPage,
} from '@/lib/sites'
import { BlogPanel } from './blog-panel'
import { HelpCard, hasSupport, type Support } from './help'
import { VersionHistory } from './history'
import { PageSwitcher } from './pages'
import { PublishModal } from './publish-modal'
import { DeviceToggle, PreviewFrame } from './renderer'
import { SettingsPanel } from './settings-panel'
import { ACCEPT, UPLOAD_HINT, uploadImage } from './upload'

// Puck is client-only and heavy: loaded when the Edit tab first opens.
const EditTab = dynamic(() => import('./edit-panel').then((m) => m.EditTab), { ssr: false })
const ItemPicker = dynamic(() => import('./item-picker').then((m) => m.ItemPicker), { ssr: false })
const PostEditor = dynamic(() => import('./post-editor').then((m) => m.PostEditor), { ssr: false })
const CHIPS = ['Add our phone number', 'Make the top section blue', 'Change the heading', 'Remove a section']
const VERBS: Record<string, string> = {
  update_block_props: 'Updating',
  add_block: 'Adding',
  remove_block: 'Removing',
  move_block: 'Moving',
}
const WITH_IMAGE = ['Use as our logo', 'Put it in the top section', 'Add to gallery']
const rupeeTime = (iso: string) =>
  new Date(iso).toLocaleString('en-IN', {
    day: 'numeric',
    month: 'short',
    hour: 'numeric',
    minute: '2-digit',
    timeZone: 'Asia/Kolkata',
  })
/** The post a finished chat turn wrote as a draft, if any (A2's blog tools: a successful step whose tool names a post). */
const draftStep = (t: Turn) => t.steps?.find((x) => x.ok && x.tool === 'post_write')
const isActive = (t: Turn) => t.status === 'queued' || t.status === 'running'
const day = (iso: string) =>
  new Date(iso).toLocaleDateString('en-IN', { day: 'numeric', month: 'short', timeZone: 'Asia/Kolkata' })
/** A chat attachment: uploaded as soon as it is chosen; Send waits for it. */
type Attach = {
  key: number
  file: File
  preview: string
  status: 'uploading' | 'done' | 'error'
  id?: string
  error?: string
}
let attachKey = 0
const imageKey = (type: string) =>
  Object.entries(blockDefs[type]?.props ?? {}).find(([, d]) => d.kind === 'image')?.[0]

function progress(t: Turn) {
  const s = t.steps?.at(-1)
  if (!s) return 'Working…'
  if (s.tool === 'set_theme') return 'Updating the look…'
  const id = typeof s.input?.id === 'string' ? s.input.id : ''
  const type = typeof s.input?.type === 'string' ? s.input.type : id ? blockType(id) : ''
  return `${VERBS[s.tool] ?? 'Updating'} ${type ? blockLabel(type) : 'the page'}…`
}

export function Builder({
  ws,
  siteId,
  canAdmin,
  domain,
  support,
}: {
  ws: string
  siteId: string
  canAdmin: boolean
  /** SITES_DOMAIN from the server; unset means publishing is unavailable. */
  domain?: string
  support: Support
}) {
  const [meta, setMeta] = useState<SiteMeta & SiteDomainMeta>({})
  const [page, setPage] = useState('') // open page; '' is Home
  const [tab, setTab] = useState<'chat' | 'edit'>('chat')
  const [histOpen, setHistOpen] = useState(false)
  const [preview, setPreview] = useState<{ version: SiteVersion; data: SiteData } | null>(null)
  const [epoch, setEpoch] = useState(0) // bumps when the draft moves for any reason but the editor's own save
  const editVer = useRef('')
  const [settings, setSettings] = useState(false)
  const [pubOpen, setPubOpen] = useState(false)
  const [publishing, setPublishing] = useState(false)
  const [toast, setToast] = useState<{ ok: boolean; text: string; url?: string; retry?: boolean }>()
  const [attach, setAttach] = useState<Attach[]>([])
  const [dragging, setDragging] = useState(false)
  const [leadsN, setLeadsN] = useState(0)
  const [help, setHelp] = useState(false)
  const [lowOff, setLowOff] = useState(false)
  const [limitMsg, setLimitMsg] = useState<string>()
  const [altOpen, setAltOpen] = useState(false)
  const [alt, setAlt] = useState('')
  const replaceInput = useRef<HTMLInputElement>(null)
  const attachInput = useRef<HTMLInputElement>(null)
  const [site, setSite] = useState<Site>()
  const [data, setData] = useState<SiteData>()
  const [turns, setTurns] = useState<Turn[]>([])
  const [redo, setRedo] = useState<string[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [mobile, setMobile] = useState(false)
  const [text, setText] = useState('')
  const [sheet, setSheet] = useState(false)
  const [err, setErr] = useState<Error>()
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState<Turn | null>(null)
  // Phase 3a: catalogue picker and price banner, blog mode and the open post.
  const [catalog, setCatalog] = useState<CatalogView | null>(null)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [syncOpen, setSyncOpen] = useState(false)
  const [syncBusy, setSyncBusy] = useState(false)
  const [syncErr, setSyncErr] = useState<Error>()
  const [blog, setBlog] = useState(false)
  const [openPost, setOpenPost] = useState<{ id: string; key: string; ai?: boolean } | null>(null)
  const [blogTick, setBlogTick] = useState(0)
  const end = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLTextAreaElement>(null)

  const load = useCallback(async () => {
    try {
      const r = await siteApi.get(ws, siteId)
      setSite(r.site)
      setData(r.data)
      setMeta({
        images: r.images,
        ai_edits: r.ai_edits,
        can_publish: r.can_publish,
        lead_space: r.lead_space,
        domains: (r as SiteDomainMeta).domains,
        domains_available: (r as SiteDomainMeta).domains_available,
      })
      setTurns([...r.turns].sort((a, b) => a.created_at.localeCompare(b.created_at)))
      // The catalogue is optional: a site without items, or a server without it, just has no banner.
      catalogApi.get(ws, siteId).then(setCatalog, () => setCatalog(null))
    } catch (e) {
      setErr(e as Error)
    }
  }, [ws, siteId])
  useEffect(() => {
    load()
  }, [load])

  // Website enquiries from the last 7 days: Leads rows whose source is "website".
  const subdomain = site?.subdomain
  useEffect(() => {
    if (!subdomain) return
    cmd<{ source: string | null; created_at: string }[]>(ws, 'followup.list', {}).then(
      (rows) => {
        const since = Date.now() - 7 * 864e5
        setLeadsN(rows.filter((d) => d.source === 'website' && Date.parse(d.created_at) >= since).length)
      },
      () => {},
    )
  }, [ws, subdomain])
  useEffect(() => {
    if (!toast?.ok) return
    const t = setTimeout(() => setToast(undefined), 8000)
    return () => clearTimeout(t)
  }, [toast])

  // A page that no longer exists (deleted, restored over) sends the person back to Home.
  const pageGone = !!page && !!data && !data.pages?.some((p) => p.slug === page)
  useEffect(() => {
    if (pageGone) setPage('')
  }, [pageGone])
  const draftId = site?.draft_version_id
  useEffect(() => {
    if (draftId && draftId !== editVer.current) setEpoch((e) => e + 1)
    if (draftId) editVer.current = draftId
  }, [draftId])

  const active = turns.find(isActive)
  const activeId = active?.id
  // Poll the active turn every second; when it ends, reload the page and the thread.
  useEffect(() => {
    if (!activeId) return
    const iv = setInterval(async () => {
      try {
        const { turn } = await siteApi.turn(ws, activeId)
        setTurns((ts) => ts.map((t) => (t.id === turn.id ? turn : t)))
        if (!isActive(turn)) {
          if (turn.version_id) setRedo([])
          load()
          if (turn.kind === 'post') {
            setBlogTick((n) => n + 1)
            setOpenPost((p) => (p && p.id === turn.post_id ? { ...p, key: `${p.id}:${Date.now()}` } : p))
          }
        }
      } catch {
        // a failed poll is retried on the next tick
      }
    }, 1000)
    return () => clearInterval(iv)
  }, [ws, activeId, load])

  // biome-ignore lint/correctness/useExhaustiveDependencies: scroll on any thread change
  useEffect(() => {
    end.current?.scrollIntoView?.({ block: 'end' })
  }, [turns, sheet])

  // The blocks of the open page (Home, or Header + page + footer); `own` is what move_block's index counts.
  const shown = data ? (viewOfPage(data, page) ?? data.content) : []
  const own = data ? (page ? (data.pages?.find((p) => p.slug === page)?.content ?? []) : data.content) : []
  const blocks = own
  const selBlock = shown.find((b) => b.props?.id === selected)
  const selType = selBlock?.type

  const uploading = attach.some((a) => a.status === 'uploading')
  const send = async (message: string, blockId?: string) => {
    if (!site || !message.trim() || active || uploading || preview) return
    setErr(undefined)
    const ids = attach.flatMap((a) => (a.id ? [a.id] : []))
    const post = message.trim().startsWith('Write a post about')
      ? 'new'
      : blog && openPost
        ? openPost.id
        : undefined
    try {
      await siteApi.chat(
        ws,
        siteId,
        site.draft_version_id,
        message.trim(),
        blockId,
        ids.length ? ids : undefined,
        post ? undefined : page || undefined,
        post,
      )
      setText('')
      setAttach([])
      await load()
    } catch (e) {
      // The monthly allowance reads as an assistant message, not an error.
      if ((e as CmdFailure).code === 'plan_limit') {
        setLimitMsg((e as Error).message)
        load()
      } else setErr(e as Error)
    }
  }

  const startUpload = (a: Attach) => {
    setAttach((l) => l.map((x) => (x.key === a.key ? { ...x, status: 'uploading', error: undefined } : x)))
    uploadImage(ws, siteId, a.file).then(
      (img) => setAttach((l) => l.map((x) => (x.key === a.key ? { ...x, status: 'done', id: img.id } : x))),
      (e: Error) =>
        setAttach((l) => l.map((x) => (x.key === a.key ? { ...x, status: 'error', error: e.message } : x))),
    )
  }
  const addFiles = (files: FileList | File[]) => {
    const room = 4 - attach.length
    for (const file of Array.from(files).slice(0, Math.max(0, room))) {
      const a: Attach = { key: ++attachKey, file, preview: URL.createObjectURL(file), status: 'uploading' }
      setAttach((l) => [...l, a])
      startUpload(a)
    }
  }
  const dropAttach = (key: number) =>
    setAttach((l) => {
      const a = l.find((x) => x.key === key)
      if (a) URL.revokeObjectURL(a.preview)
      return l.filter((x) => x.key !== key)
    })

  // Publish again from the top bar: no modal, a toast says what happened.
  const republish = async () => {
    if (!site) return
    setPublishing(true)
    setToast(undefined)
    try {
      const r = await siteApi.publish(ws, siteId, site.draft_version_id)
      setSite(r.site)
      setToast({ ok: true, text: 'Changes are live within a minute', url: r.url })
    } catch (e) {
      const f = e as CmdFailure
      if (f.code === 'forbidden' && /verify/i.test(f.message)) {
        await load()
        setPubOpen(true)
      } else if (f.code === 'conflict') setErr(f)
      else
        setToast({
          ok: false,
          text: `Couldn't publish. Your live site is unchanged.${f.message ? ` ${f.message}` : ''}`,
          retry: true,
        })
    } finally {
      setPublishing(false)
    }
  }

  const apply = async (
    fn: () => Promise<{ site: Site; data: SiteData }>,
    keepRedo = false,
  ): Promise<boolean> => {
    setBusy(true)
    setErr(undefined)
    try {
      const r = await fn()
      setSite(r.site)
      setData(r.data)
      if (!keepRedo) setRedo([])
      if (selected && !viewOfPage(r.data, page)?.some((b) => b.props?.id === selected)) setSelected(null)
      return true
    } catch (e) {
      setErr(e as Error)
      return false
    } finally {
      setBusy(false)
    }
  }
  const edit = (ops: EditOp[]) => site && apply(() => siteApi.edit(ws, siteId, site.draft_version_id, ops))
  const restore = (vid: string, keepRedo = false) =>
    site && apply(() => siteApi.restore(ws, siteId, site.draft_version_id, vid), keepRedo)

  const idx = blocks.findIndex((b) => b.props?.id === selected)
  const foreign = !!selected && idx < 0 // a shared block seen on another page: change it on Home
  const imgProp = selType ? imageKey(selType) : undefined
  const altProp =
    imgProp && blockDefs[selType as string]?.props?.[`${imgProp}_alt`] ? `${imgProp}_alt` : undefined
  const replaceImage = async (file: File) => {
    if (!selected || !imgProp) return
    setBusy(true)
    setErr(undefined)
    try {
      const img = await uploadImage(ws, siteId, file)
      setBusy(false)
      await edit([{ tool: 'update_block_props', input: { id: selected, props: { [imgProp]: img.id } } }])
    } catch (e) {
      setErr(e as Error)
      setBusy(false)
    }
  }
  const toolbar = selBlock && selected && (
    <div
      className="flex items-center gap-1 border-b border-line bg-canvas px-3 py-1.5"
      role="toolbar"
      aria-label={`${blockLabel(selBlock.type)} actions`}
    >
      <span className="mr-auto text-[12px] font-medium">{blockLabel(selBlock.type)}</span>
      {imgProp && (
        <button
          type="button"
          title={`Replace image. ${UPLOAD_HINT}`}
          aria-label={`Replace image in ${blockLabel(selBlock.type)}`}
          disabled={busy || !!active}
          onClick={() => replaceInput.current?.click()}
          className="rounded p-1.5 hover:bg-hover disabled:opacity-40"
        >
          <Replace size={15} />
        </button>
      )}
      {altProp && (
        <button
          type="button"
          title="Describe photo"
          aria-label="Describe photo"
          aria-pressed={altOpen}
          disabled={busy || !!active}
          onClick={() => {
            setAlt(String(selBlock.props?.[altProp] ?? ''))
            setAltOpen((o) => !o)
          }}
          className="rounded p-1.5 hover:bg-hover disabled:opacity-40"
        >
          <Type size={15} />
        </button>
      )}
      {(
        [
          [
            'Move up',
            ArrowUp,
            idx <= 0,
            () => edit([{ tool: 'move_block', input: { id: selected, index: idx - 1 } }]),
          ],
          [
            'Move down',
            ArrowDown,
            idx >= blocks.length - 1,
            () => edit([{ tool: 'move_block', input: { id: selected, index: idx + 1 } }]),
          ],
          [
            'Duplicate',
            Copy,
            (blockDefs[selBlock.type]?.max ?? 99) <= blocks.filter((b) => b.type === selBlock.type).length,
            () => {
              const { id: _id, ...props } = selBlock.props
              return edit([
                {
                  tool: 'add_block',
                  input: { type: selBlock.type, props, index: idx + 1, ...(page ? { page } : {}) },
                },
              ])
            },
          ],
          ['Delete', Trash2, false, () => edit([{ tool: 'remove_block', input: { id: selected } }])],
        ] as const
      ).map(([label, Icon, off, run]) => (
        <button
          key={label}
          type="button"
          aria-label={`${label} ${blockLabel(selBlock.type)}`}
          title={label}
          disabled={off || foreign || busy || !!active}
          onClick={run}
          className="rounded p-1.5 hover:bg-hover disabled:opacity-40"
        >
          <Icon size={15} />
        </button>
      ))}
    </div>
  )

  if (!site || !data)
    return err ? <ErrorText error={err} /> : <p className="p-6 text-[13px] text-muted">Loading…</p>

  const parent = site.parent_version_id
  const changes = turns.filter((t) => t.version_id)
  const newest = changes.at(-1)
  const latest = newest?.version_id === site.draft_version_id ? newest : undefined
  const card = (t: Turn) => {
    const isNewest = t === newest
    const applied = isNewest && t.version_id === site.draft_version_id
    const undone = isNewest && t.base_version_id === site.draft_version_id
    const older = !isNewest && t.version_id !== site.draft_version_id
    return (
      <div
        className={`flex items-center gap-2 rounded-lg border border-line p-2.5 ${undone ? 'text-muted' : ''}`}
      >
        <Check size={15} aria-hidden="true" className={undone ? '' : 'text-ok'} />
        <p className="min-w-0 flex-1 text-[13px]">{t.summary || 'Updated your site'}</p>
        {applied && (
          <button
            type="button"
            className={btn}
            disabled={busy || !!active}
            onClick={() => restore(t.base_version_id)}
          >
            Undo
          </button>
        )}
        {older && (
          <button type="button" className={btn} disabled={busy || !!active} onClick={() => setConfirm(t)}>
            Restore to here
          </button>
        )}
        {undone && (
          <>
            <span className="text-[12px]">Undone</span>
            <button
              type="button"
              className={btn}
              disabled={busy || !!active}
              onClick={() => restore(t.version_id as string)}
            >
              Redo
            </button>
          </>
        )}
      </div>
    )
  }

  const lastId = turns.at(-1)?.id
  const pubId = site.published_version_id
  const status = site.taken_down
    ? 'offline'
    : !pubId
      ? site.subdomain
        ? 'offline'
        : 'draft'
      : pubId === site.draft_version_id
        ? 'published'
        : 'changes'
  const ai = meta.ai_edits
  const left = ai ? Math.max(0, ai.limit - ai.used) : undefined
  const locked = left === 0
  const resets = ai ? day(ai.resets_at) : ''
  const lastTwoFailed = turns.length >= 2 && turns.slice(-2).every((t) => t.status === 'failed')
  const helpOn = hasSupport(support)
  const needsModal =
    !site.subdomain ||
    !domain ||
    meta.can_publish?.configured === false ||
    meta.can_publish?.verified === false
  const publishBtn =
    status === 'published' ? (
      <button type="button" className={btn} onClick={() => setPubOpen(true)}>
        Published <Check size={14} aria-hidden="true" />
      </button>
    ) : (
      <button
        type="button"
        className={btnPrimary}
        disabled={!canAdmin || publishing || !!active || !!site.taken_down}
        title={canAdmin ? undefined : 'Ask an admin to publish'}
        onClick={() => (needsModal ? setPubOpen(true) : republish())}
      >
        {publishing && <Loader2 size={14} className="animate-spin" aria-hidden="true" />}
        {status === 'changes' ? 'Publish changes' : 'Publish'}
      </button>
    )
  // The preview iframe draws the Catalogue block from this snapshot (what catalog_get returns, priced as visitors see it).
  const catalogProps = catalog
    ? { catalog: catalogueView(previewSnapshot(catalog.items, catalog.price_mode)) }
    : {}
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line px-3">
        <Link href={`/w/${ws}`} className="rounded p-1.5 hover:bg-hover" aria-label="Back">
          <ArrowLeft size={16} />
        </Link>
        <h1 className="min-w-0 flex-1 truncate text-[14px] font-semibold md:flex-none">{site.name}</h1>
        <PageSwitcher
          ws={ws}
          site={site}
          data={data}
          page={page}
          onPage={(slug) => {
            setPage(slug)
            setSelected(null)
          }}
          apply={apply}
          disabled={busy || !!active || !!preview}
        />
        <button
          type="button"
          aria-pressed={blog}
          title="Blog posts"
          className={`${btn} ${blog ? 'bg-hover' : ''}`}
          onClick={() => {
            setBlog((b) => !b)
            setOpenPost(null)
            setTab('edit')
          }}
        >
          <Newspaper size={14} aria-hidden="true" /> Blog
        </button>
        {status === 'draft' && <Pill>Draft</Pill>}
        {status === 'published' && <Pill tone="ok">Published</Pill>}
        {status === 'changes' && (
          <Pill tone="warn">
            <span aria-hidden="true" className="mr-1 inline-block size-1.5 rounded-full bg-warn" />
            Unpublished changes
          </Pill>
        )}
        {status === 'offline' && <Pill tone="bad">Offline</Pill>}
        {leadsN > 0 && (
          <Link
            href={`/w/${ws}/crm/inbox`}
            className="inline-flex items-center gap-1 rounded-full border border-line px-2 py-0.5 text-[12px] hover:bg-hover"
          >
            <Mail size={12} aria-hidden="true" /> {leadsN} new {leadsN === 1 ? 'enquiry' : 'enquiries'}
          </Link>
        )}
        <div className="flex gap-0.5 md:mr-auto">
          <button
            type="button"
            aria-label="Undo"
            className="rounded p-1.5 hover:bg-hover disabled:opacity-40"
            disabled={!parent || busy || !!active}
            onClick={() => {
              setRedo((r) => [...r, site.draft_version_id])
              restore(parent as string, true)
            }}
          >
            <Undo2 size={16} />
          </button>
          <button
            type="button"
            aria-label="Redo"
            className="rounded p-1.5 hover:bg-hover disabled:opacity-40"
            disabled={!redo.length || busy || !!active}
            onClick={() => {
              const v = redo[redo.length - 1]
              setRedo((r) => r.slice(0, -1))
              restore(v, true)
            }}
          >
            <Redo2 size={16} />
          </button>
        </div>
        <button
          type="button"
          aria-label="Version history"
          title="Version history"
          aria-pressed={histOpen}
          className="rounded p-1.5 hover:bg-hover"
          onClick={() => {
            setHistOpen((o) => !o)
            if (histOpen) setPreview(null)
          }}
        >
          <Clock size={16} />
        </button>
        <button
          type="button"
          aria-label="Website settings"
          title="Website settings"
          className="rounded p-1.5 hover:bg-hover"
          onClick={() => setSettings(true)}
        >
          <Settings size={16} />
        </button>
        {publishBtn}
      </header>
      {site.taken_down && (
        <p role="alert" className="border-b border-line bg-bad-bg px-3 py-2 text-[13px] text-bad">
          This website was taken offline by C Note
          {site.taken_down.reason ? `: ${site.taken_down.reason}` : '.'} Contact us if you think this is a
          mistake.
        </p>
      )}
      {catalog && catalog.items.length > 0 && !!site.published_version_id && (
        <p
          role="status"
          className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-1.5 text-[12px]"
        >
          {catalog.changed > 0 ? (
            <>
              <span className="text-warn">
                {catalog.changed} {catalog.changed === 1 ? 'price' : 'prices'} changed since you published
              </span>
              <button
                type="button"
                className={btn}
                disabled={!canAdmin}
                title={canAdmin ? undefined : 'Ask an admin to update prices'}
                onClick={() => {
                  setSyncErr(undefined)
                  setSyncOpen(true)
                }}
              >
                Update prices
              </button>
            </>
          ) : (
            <span className="text-muted">Your website's prices are up to date.</span>
          )}
          {catalog.synced_at && (
            <span className="text-muted">Prices as of {rupeeTime(catalog.synced_at)}</span>
          )}
        </p>
      )}
      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <section className="order-1 flex min-h-0 flex-1 flex-col bg-hover p-0 md:order-2 md:p-4">
          <div className="flex min-h-0 flex-1 flex-col overflow-hidden border-line bg-canvas md:rounded-lg md:border">
            <div className="flex items-center gap-3 border-b border-line px-3 py-1.5">
              <span aria-hidden="true" className="flex gap-1">
                <i className="h-2.5 w-2.5 rounded-full bg-line" />
                <i className="h-2.5 w-2.5 rounded-full bg-line" />
                <i className="h-2.5 w-2.5 rounded-full bg-line" />
              </span>
              <span className="flex min-w-0 flex-1 items-center gap-1.5 truncate rounded bg-hover px-2 py-0.5 text-[12px] text-muted">
                <Globe size={12} aria-hidden="true" />{' '}
                {site.url ? site.url.replace(/^https?:\/\//, '') : 'Not published yet'}
              </span>
              <span className="max-md:hidden">
                <DeviceToggle mobile={mobile} onChange={setMobile} />
              </span>
            </div>
            {(page === 'privacy' || page === 'terms') && (
              <p className="border-b border-line bg-warn-bg px-3 py-1.5 text-[12px] text-warn">
                Template — not legal advice. Read it and have it checked before you publish.
              </p>
            )}
            {preview && (
              <p role="status" className="border-b border-line bg-warn-bg px-3 py-1.5 text-[12px] text-warn">
                Viewing an earlier version. Your live site is not changed.
              </p>
            )}
            {!preview && toolbar}
            {altOpen && altProp && selected && (
              <form
                className="flex items-end gap-2 border-b border-line bg-canvas px-3 py-2"
                onSubmit={(e) => {
                  e.preventDefault()
                  edit([
                    { tool: 'update_block_props', input: { id: selected, props: { [altProp]: alt.trim() } } },
                  ])
                  setAltOpen(false)
                }}
              >
                <label className="min-w-0 flex-1 text-[12px] font-medium text-muted">
                  Describe this photo for people who can't see it
                  <input
                    value={alt}
                    maxLength={120}
                    onChange={(e) => setAlt(e.target.value)}
                    className="mt-1 w-full rounded-md border border-line bg-canvas px-2.5 py-1.5 text-[14px] font-normal text-fg"
                  />
                </label>
                <button type="submit" className={btnPrimary}>
                  Save
                </button>
              </form>
            )}
            {blog && openPost ? (
              <div className="flex min-h-0 flex-1 flex-col overflow-hidden bg-canvas">
                <PostEditor
                  key={openPost.key}
                  ws={ws}
                  site={site}
                  postId={openPost.id}
                  images={meta.images ?? []}
                  ai={openPost.ai}
                  onImage={(img) =>
                    setMeta((m) => ({
                      ...m,
                      images: [...(m.images ?? []).filter((i) => i.id !== img.id), img],
                    }))
                  }
                  onChanged={() => setBlogTick((n) => n + 1)}
                  onOpen={(id) => setOpenPost((o) => (o ? { ...o, id } : o))}
                  onClose={() => setOpenPost(null)}
                />
              </div>
            ) : (
              <div className="min-h-0 flex-1 overflow-hidden bg-hover">
                <PreviewFrame
                  data={dataOfPage(preview ? preview.data : data, page)}
                  selected={selected}
                  onSelect={preview ? undefined : setSelected}
                  mobile={mobile}
                  className="h-full"
                  assetBase={`/api/w/${ws}/sites/${siteId}/img`}
                  pro={!!site.pro}
                  {...catalogProps}
                />
              </div>
            )}
            <div aria-hidden="true" className={`h-0.5 shrink-0 ${active ? 'animate-pulse bg-accent' : ''}`} />
          </div>
        </section>

        <aside
          className="relative order-2 flex shrink-0 flex-col border-t border-line bg-canvas md:order-1 md:min-h-0 md:w-[380px] md:border-r md:border-t-0"
          onDragOver={(e) => {
            if (e.dataTransfer.types.includes('Files')) {
              e.preventDefault()
              setDragging(true)
            }
          }}
          onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDragging(false)}
          onDrop={(e) => {
            e.preventDefault()
            setDragging(false)
            if (!locked) addFiles(e.dataTransfer.files)
          }}
        >
          {dragging && (
            <div className="pointer-events-none absolute inset-2 z-10 flex items-center justify-center rounded-lg border-2 border-dashed border-accent bg-canvas/90 text-[13px] font-medium">
              Drop image to add it
            </div>
          )}
          <div
            role="tablist"
            aria-label="Chat or Edit"
            className="flex gap-1 border-b border-line px-3 py-1.5"
          >
            {(['chat', 'edit'] as const).map((t) => (
              <button
                key={t}
                type="button"
                role="tab"
                aria-selected={tab === t}
                className={`rounded-md px-3 py-1 text-[13px] font-medium ${tab === t ? 'bg-hover' : 'text-muted hover:bg-hover'}`}
                onClick={() => setTab(t)}
              >
                {t === 'chat' ? 'Chat' : 'Edit'}
              </button>
            ))}
          </div>
          {tab === 'edit' && blog && (
            <div className="flex min-h-0 flex-1 flex-col max-md:max-h-[55vh]">
              <BlogPanel
                ws={ws}
                siteId={siteId}
                data={data}
                openId={openPost?.id ?? null}
                tick={blogTick}
                onOpen={(id) => setOpenPost(id ? { id, key: `${id}:${Date.now()}` } : null)}
                onWrite={() => {
                  setTab('chat')
                  setText('Write a post about ')
                  input.current?.focus()
                }}
              />
            </div>
          )}
          {tab === 'edit' && !blog && (
            <div className="flex min-h-0 flex-1 flex-col max-md:max-h-[55vh]">
              <div className="flex items-center gap-2 border-b border-line px-3 py-1.5 text-[12px]">
                <Package size={14} aria-hidden="true" className="text-muted" />
                <span className="flex-1 text-muted">
                  {catalog?.items.length
                    ? `${catalog.items.length} items on your website`
                    : 'Products on your website'}
                </span>
                <button
                  type="button"
                  className={btn}
                  disabled={!!active || !!preview}
                  onClick={() => setPickerOpen(true)}
                >
                  Choose items
                </button>
              </div>
              <EditTab
                key={`${page}:${epoch}:${preview?.version.id ?? ''}`}
                ws={ws}
                siteId={siteId}
                page={page}
                versionId={site.draft_version_id}
                revision={site.rev}
                data={preview ? preview.data : data}
                images={meta.images ?? []}
                readOnly={!!active || !!preview}
                onSaved={(r) => {
                  editVer.current = r.site.draft_version_id // our own save: no remount
                  setSite(r.site)
                  setData(r.data)
                  setRedo([])
                }}
                onReload={load}
              />
            </div>
          )}
          <div className={tab === 'chat' ? 'contents' : 'hidden'}>
            {helpOn && (
              <div className="flex items-center justify-end border-b border-line px-3 py-1">
                <button
                  type="button"
                  aria-label="Talk to a person"
                  aria-pressed={help}
                  title="Talk to a person"
                  className="rounded p-1 text-muted hover:bg-hover"
                  onClick={() => setHelp((h) => !h)}
                >
                  <HelpCircle size={15} />
                </button>
              </div>
            )}
            <div
              className={`min-h-0 flex-1 space-y-3 overflow-y-auto p-3 ${sheet ? 'max-md:max-h-[55vh]' : 'max-md:hidden'}`}
            >
              {turns.length === 0 && (
                <EmptyState compact icon={MessageSquare} title="Your site is ready. Tell me what to change.">
                  Try one of the suggestions below.
                </EmptyState>
              )}
              {turns.map((t) => (
                <div key={t.id} className="space-y-2">
                  <div className="ml-8 whitespace-pre-wrap rounded-lg bg-hover px-3 py-2 text-[13px]">
                    {t.message}
                  </div>
                  {isActive(t) && (
                    <p className="flex items-center gap-2 text-[13px] text-muted" role="status">
                      <Loader2 size={14} className="animate-spin" /> {progress(t)}
                    </p>
                  )}
                  {t.status === 'cancelled' && <p className="text-[13px] text-muted">Stopped.</p>}
                  {t.status === 'failed' && (
                    <div role="alert" className="space-y-1.5 rounded-lg bg-bad-bg p-2.5 text-[13px] text-bad">
                      <p>Couldn't apply that. Your site wasn't changed.</p>
                      {/\[(PHONE|EMAIL|GSTIN|PAN|AADHAAR)\]|redact/i.test(t.error ?? '') ? (
                        <p>To change contact details, edit them in Settings, Company profile.</p>
                      ) : (
                        t.error && <p className="opacity-80">{t.error}</p>
                      )}
                      {t.id === lastId && (
                        <button
                          type="button"
                          className={btn}
                          onClick={() => send(t.message, t.block_id ?? undefined)}
                        >
                          Retry
                        </button>
                      )}
                    </div>
                  )}
                  {t.status === 'done' && t.reply && (
                    <p className="whitespace-pre-wrap pr-8 text-[13px]">{t.reply}</p>
                  )}
                  {t.status === 'done' && t.version_id && card(t)}
                  {t.status === 'done' &&
                    (() => {
                      const d = draftStep(t)
                      if (!d) return null
                      const pid = typeof d.input?.post_id === 'string' ? d.input.post_id : ''
                      return (
                        <div className="flex items-center gap-2 rounded-lg border border-line p-2.5">
                          <Newspaper size={15} aria-hidden="true" />
                          <p className="min-w-0 flex-1 text-[13px]">
                            A draft post is ready. Read it before you publish.
                          </p>
                          <button
                            type="button"
                            className={btn}
                            onClick={() => {
                              setBlog(true)
                              setTab('edit')
                              setBlogTick((n) => n + 1)
                              setOpenPost(pid ? { id: pid, key: `${pid}:ai`, ai: true } : null)
                            }}
                          >
                            Review draft
                          </button>
                        </div>
                      )
                    })()}
                </div>
              ))}
              {locked && (
                <p role="status" className="whitespace-pre-wrap pr-8 text-[13px]">
                  {limitMsg ??
                    `You've used all ${ai?.limit} AI edits for this month. Your website stays live and enquiries keep coming. You can still move, duplicate and delete sections, replace images and undo. Edits come back on ${resets}. More edits will come with a paid plan.`}
                </p>
              )}
              {helpOn && (help || lastTwoFailed) && (
                <HelpCard support={support} site={site.url ?? site.name} />
              )}
              <div ref={end} />
            </div>
            {latest && !sheet && <div className="px-3 pt-2 md:hidden">{card(latest)}</div>}
            <div className="space-y-2 p-3">
              {left !== undefined && left > 0 && left <= 5 && !lowOff && (
                <p
                  role="status"
                  className="flex items-center gap-2 rounded-md bg-warn-bg px-2.5 py-1.5 text-[12px] text-warn"
                >
                  <span className="flex-1">
                    {left} {left === 1 ? 'edit' : 'edits'} left this month. Resets {resets}.
                  </span>
                  <button type="button" aria-label="Dismiss" onClick={() => setLowOff(true)}>
                    <X size={12} />
                  </button>
                </p>
              )}
              {attach.length > 0 && (
                <ul className="flex flex-wrap gap-2" aria-label="Attached photos">
                  {attach.map((a) => (
                    <li
                      key={a.key}
                      className={`relative size-12 overflow-hidden rounded-md border ${a.status === 'error' ? 'border-bad' : 'border-line'}`}
                      title={a.error}
                    >
                      {/* biome-ignore lint/performance/noImgElement: a local blob preview */}
                      <img src={a.preview} alt="" className="size-full object-cover" />
                      {a.status === 'uploading' && (
                        <span
                          role="status"
                          aria-label="Uploading"
                          className="absolute inset-0 flex items-center justify-center bg-canvas/70"
                        >
                          <Loader2 size={16} className="animate-spin" />
                        </span>
                      )}
                      {a.status === 'error' && (
                        <button
                          type="button"
                          aria-label="Couldn't upload. Try again"
                          className="absolute inset-0 flex items-center justify-center bg-bad-bg text-[10px] font-medium text-bad"
                          onClick={() => startUpload(a)}
                        >
                          Retry
                        </button>
                      )}
                      <button
                        type="button"
                        aria-label="Remove photo"
                        className="absolute right-0 top-0 rounded-bl bg-canvas p-0.5"
                        onClick={() => dropAttach(a.key)}
                      >
                        <X size={10} />
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              {attach.some((a) => a.status === 'error') && (
                <p role="alert" className="text-[12px] text-bad">
                  Couldn't upload. {attach.find((a) => a.error)?.error}
                </p>
              )}
              {attach.length > 0 && <p className="text-[12px] text-muted">{UPLOAD_HINT}</p>}
              {!locked && (
                <ul className="-mx-3 flex gap-2 overflow-x-auto px-3 pb-1">
                  {!attach.length && (
                    <>
                      <li className="shrink-0">
                        <button
                          type="button"
                          className="rounded-full border border-line px-2.5 py-1 text-[12px] hover:bg-hover"
                          onClick={() => setPickerOpen(true)}
                        >
                          Show my products on the site
                        </button>
                      </li>
                      <li className="shrink-0">
                        <button
                          type="button"
                          className="rounded-full border border-line px-2.5 py-1 text-[12px] hover:bg-hover"
                          onClick={() => {
                            setText('Write a post about ')
                            input.current?.focus()
                          }}
                        >
                          Write a post about…
                        </button>
                      </li>
                    </>
                  )}
                  {(attach.length ? WITH_IMAGE : CHIPS).map((c) => (
                    <li key={c} className="shrink-0">
                      <button
                        type="button"
                        className="rounded-full border border-line px-2.5 py-1 text-[12px] hover:bg-hover"
                        onClick={() => {
                          setText(c)
                          input.current?.focus()
                        }}
                      >
                        {c}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
              {(err as { code?: string } | undefined)?.code === 'conflict' ? (
                <p role="alert" className="flex items-center gap-2 text-[13px] text-bad">
                  The site changed. Reload to see the latest.
                  <button
                    type="button"
                    className={btn}
                    onClick={() => {
                      setErr(undefined)
                      load()
                    }}
                  >
                    Reload
                  </button>
                </p>
              ) : (
                <ErrorText error={err} />
              )}
              <form
                className="rounded-lg border border-line focus-within:border-accent"
                onSubmit={(e) => {
                  e.preventDefault()
                  send(text, selected ?? undefined)
                }}
              >
                {selected && selType && (
                  <div className="px-2 pt-2">
                    <span className="inline-flex items-center gap-1 rounded-full bg-hover px-2 py-0.5 text-[12px]">
                      Editing: {blockLabel(selType)}
                      <button
                        type="button"
                        aria-label="Stop editing this section"
                        onClick={() => setSelected(null)}
                      >
                        <X size={12} />
                      </button>
                    </span>
                  </div>
                )}
                <div className="flex items-end gap-1 p-1.5">
                  <button
                    type="button"
                    aria-label={sheet ? 'Hide conversation' : 'Show conversation'}
                    aria-pressed={sheet}
                    className="rounded p-1.5 hover:bg-hover md:hidden"
                    onClick={() => setSheet((s) => !s)}
                  >
                    <MessageSquare size={16} />
                  </button>
                  <button
                    type="button"
                    aria-label="Add a photo"
                    title={UPLOAD_HINT}
                    disabled={locked || attach.length >= 4}
                    className="rounded p-1.5 hover:bg-hover disabled:opacity-40"
                    onClick={() => attachInput.current?.click()}
                  >
                    <ImagePlus size={16} />
                  </button>
                  <textarea
                    ref={input}
                    aria-label="Ask for a change"
                    rows={2}
                    value={text}
                    disabled={locked}
                    placeholder={locked ? `Edits are back on ${resets}` : 'Ask for a change…'}
                    onChange={(e) => setText(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && !e.shiftKey) {
                        e.preventDefault()
                        send(text, selected ?? undefined)
                      }
                    }}
                    className="min-w-0 flex-1 resize-none bg-transparent px-1.5 py-1 text-[14px] placeholder:text-muted focus:outline-none"
                  />
                  {active ? (
                    <button
                      type="button"
                      aria-label="Stop"
                      className="rounded-md bg-hover p-2"
                      onClick={() => siteApi.cancel(ws, active.id).then(load, (e) => setErr(e))}
                    >
                      <Square size={14} />
                    </button>
                  ) : (
                    <button
                      type="submit"
                      aria-label="Send"
                      disabled={!text.trim() || locked || uploading}
                      className="rounded-md bg-accent p-2 text-accent-fg disabled:opacity-50"
                    >
                      <Send size={14} />
                    </button>
                  )}
                </div>
              </form>
              {left !== undefined && (
                <p
                  className="text-[12px] text-muted"
                  title="Each change I make uses 1 edit. Moving, deleting and Undo are free."
                >
                  {left} {left === 1 ? 'edit' : 'edits'} left this month
                </p>
              )}
            </div>
          </div>
        </aside>
      </div>
      <input
        ref={attachInput}
        type="file"
        accept={ACCEPT}
        multiple
        hidden
        data-testid="attach-input"
        onChange={(e) => {
          if (e.target.files) addFiles(e.target.files)
          e.target.value = ''
        }}
      />
      <input
        ref={replaceInput}
        type="file"
        accept={ACCEPT}
        hidden
        data-testid="replace-input"
        onChange={(e) => {
          const f = e.target.files?.[0]
          e.target.value = ''
          if (f) replaceImage(f)
        }}
      />
      {toast && (
        <div
          role="status"
          className={`fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-3 rounded-lg px-3 py-2 text-[13px] shadow-lg ${toast.ok ? 'bg-fg text-canvas' : 'bg-bad-bg text-bad'}`}
        >
          <span>{toast.text}</span>
          {toast.url && (
            <a href={toast.url} target="_blank" rel="noopener noreferrer" className="underline">
              Visit
            </a>
          )}
          {toast.retry && (
            <button type="button" className="underline" onClick={republish}>
              Retry
            </button>
          )}
          <button type="button" aria-label="Dismiss" onClick={() => setToast(undefined)}>
            <X size={12} />
          </button>
        </div>
      )}
      {histOpen && (
        <aside
          aria-label="Version history"
          className="fixed inset-y-0 right-0 z-40 w-[480px] max-w-full border-l border-line bg-canvas shadow-xl"
        >
          <VersionHistory
            ws={ws}
            siteId={siteId}
            draftVersionId={site.draft_version_id}
            onPreview={setPreview}
            onRestored={(r) => {
              setPreview(null)
              setSite(r.site)
              setData(r.data)
              setRedo([])
            }}
            onReload={() => {
              setPreview(null)
              load()
            }}
            onClose={() => {
              setPreview(null)
              setHistOpen(false)
            }}
          />
        </aside>
      )}
      {settings && (
        <SettingsPanel
          key={page}
          ws={ws}
          page={page}
          domains={meta.domains ?? []}
          domainsAvailable={!!meta.domains_available}
          onReload={load}
          site={site}
          data={data}
          meta={meta}
          canAdmin={canAdmin}
          onClose={() => setSettings(false)}
          onEdit={async (ops) => {
            await siteApi.edit(ws, siteId, site.draft_version_id, ops).then((r) => {
              setSite(r.site)
              setData(r.data)
            })
          }}
          onSite={setSite}
        />
      )}
      <PublishModal
        ws={ws}
        site={site}
        meta={meta}
        domain={domain}
        open={pubOpen}
        onClose={() => setPubOpen(false)}
        onSite={setSite}
        onReload={load}
        shareDesc={data.root?.props?.description ?? ''}
      />
      <ItemPicker
        ws={ws}
        site={site}
        data={data}
        images={meta.images ?? []}
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
        onSaved={setCatalog}
        onImage={(img) =>
          setMeta((m) => ({ ...m, images: [...(m.images ?? []).filter((i) => i.id !== img.id), img] }))
        }
      />
      <Modal open={syncOpen} onClose={() => setSyncOpen(false)} title="Update prices on your website?">
        {catalog && (
          <div className="space-y-3 text-[13px]">
            {(() => {
              const list = catalogChanges(catalog)
              return list.length ? (
                <ul className="max-h-60 list-disc space-y-1 overflow-y-auto pl-5">
                  {list.map((c) => (
                    <li key={c}>{c}</li>
                  ))}
                </ul>
              ) : (
                <p>
                  {catalog.changed} {catalog.changed === 1 ? 'item has' : 'items have'} a new price, name or
                  unit in Items.
                </p>
              )
            })()}
            {catalog.synced_at && (
              <p className="text-muted">
                Your website now shows prices as of {rupeeTime(catalog.synced_at)}.
              </p>
            )}
            <ErrorText error={syncErr} />
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={() => setSyncOpen(false)}>
                Cancel
              </button>
              <button
                type="button"
                className={btnPrimary}
                disabled={syncBusy}
                onClick={async () => {
                  setSyncBusy(true)
                  setSyncErr(undefined)
                  try {
                    await catalogApi.sync(ws, siteId)
                    setSyncOpen(false)
                    setToast({ ok: true, text: 'Prices are live within a minute' })
                    load()
                  } catch (e) {
                    setSyncErr(e as Error)
                  } finally {
                    setSyncBusy(false)
                  }
                }}
              >
                {syncBusy && <Loader2 size={14} className="animate-spin" aria-hidden="true" />} Update prices
              </button>
            </div>
          </div>
        )}
      </Modal>
      <Modal open={!!confirm} onClose={() => setConfirm(null)} title="Restore this version?">
        {confirm && (
          <div className="space-y-4">
            <p className="text-[13px]">
              This replaces {changes.length - changes.indexOf(confirm) - 1} later change
              {changes.length - changes.indexOf(confirm) - 1 === 1 ? '' : 's'}.
            </p>
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={() => setConfirm(null)}>
                Cancel
              </button>
              <button
                type="button"
                className={btnPrimary}
                onClick={() => {
                  const v = confirm.version_id as string
                  setConfirm(null)
                  restore(v)
                }}
              >
                Restore
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
