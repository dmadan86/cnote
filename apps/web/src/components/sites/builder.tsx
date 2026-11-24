'use client'
import {
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Check,
  Copy,
  Globe,
  Loader2,
  MessageSquare,
  Redo2,
  Send,
  Square,
  Trash2,
  Undo2,
  X,
} from 'lucide-react'
import Link from 'next/link'
import { useCallback, useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, EmptyState, ErrorText, Modal, Pill } from '@/components/ui'
import {
  blockDefs,
  blockLabel,
  blockType,
  type EditOp,
  type Site,
  type SiteData,
  siteApi,
  type Turn,
} from '@/lib/sites'
import { DeviceToggle, PreviewFrame } from './renderer'

const CHIPS = ['Add our phone number', 'Make the top section blue', 'Change the heading', 'Remove a section']
const VERBS: Record<string, string> = {
  update_block_props: 'Updating',
  add_block: 'Adding',
  remove_block: 'Removing',
  move_block: 'Moving',
}
const isActive = (t: Turn) => t.status === 'queued' || t.status === 'running'

function progress(t: Turn) {
  const s = t.steps?.at(-1)
  if (!s) return 'Working…'
  if (s.tool === 'set_theme') return 'Updating the look…'
  const id = typeof s.input?.id === 'string' ? s.input.id : ''
  const type = typeof s.input?.type === 'string' ? s.input.type : id ? blockType(id) : ''
  return `${VERBS[s.tool] ?? 'Updating'} ${type ? blockLabel(type) : 'the page'}…`
}

export function Builder({ ws, siteId }: { ws: string; siteId: string }) {
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
  const end = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLTextAreaElement>(null)

  const load = useCallback(async () => {
    try {
      const r = await siteApi.get(ws, siteId)
      setSite(r.site)
      setData(r.data)
      setTurns([...r.turns].sort((a, b) => a.created_at.localeCompare(b.created_at)))
    } catch (e) {
      setErr(e as Error)
    }
  }, [ws, siteId])
  useEffect(() => {
    load()
  }, [load])

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

  const blocks = data?.content ?? []
  const selBlock = blocks.find((b) => b.props?.id === selected)
  const selType = selBlock?.type

  const send = async (message: string, blockId?: string) => {
    if (!site || !message.trim() || active) return
    setErr(undefined)
    try {
      await siteApi.chat(ws, siteId, site.draft_version_id, message.trim(), blockId)
      setText('')
      await load()
    } catch (e) {
      setErr(e as Error)
    }
  }

  const apply = async (fn: () => Promise<{ site: Site; data: SiteData }>, keepRedo = false) => {
    setBusy(true)
    setErr(undefined)
    try {
      const r = await fn()
      setSite(r.site)
      setData(r.data)
      if (!keepRedo) setRedo([])
      if (selected && !r.data.content.some((b) => b.props?.id === selected)) setSelected(null)
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const edit = (ops: EditOp[]) => site && apply(() => siteApi.edit(ws, siteId, site.draft_version_id, ops))
  const restore = (vid: string, keepRedo = false) =>
    site && apply(() => siteApi.restore(ws, siteId, site.draft_version_id, vid), keepRedo)

  const idx = blocks.findIndex((b) => b.props?.id === selected)
  const toolbar = selBlock && selected && (
    <div
      className="flex items-center gap-1 border-b border-line bg-canvas px-3 py-1.5"
      role="toolbar"
      aria-label={`${blockLabel(selBlock.type)} actions`}
    >
      <span className="mr-auto text-[12px] font-medium">{blockLabel(selBlock.type)}</span>
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
              return edit([{ tool: 'add_block', input: { type: selBlock.type, props, index: idx + 1 } }])
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
          disabled={off || busy || !!active}
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
  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line px-3">
        <Link href={`/w/${ws}`} className="rounded p-1.5 hover:bg-hover" aria-label="Back">
          <ArrowLeft size={16} />
        </Link>
        <h1 className="min-w-0 flex-1 truncate text-[14px] font-semibold md:flex-none">{site.name}</h1>
        <Pill tone="warn">Draft</Pill>
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
          className={`${btn} max-md:hidden`}
          disabled
          title="Publishing arrives in the next phase"
        >
          Publish
        </button>
      </header>
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
                <Globe size={12} aria-hidden="true" /> Not published yet
              </span>
              <span className="max-md:hidden">
                <DeviceToggle mobile={mobile} onChange={setMobile} />
              </span>
            </div>
            {toolbar}
            <div className="min-h-0 flex-1 overflow-hidden bg-hover">
              <PreviewFrame
                data={data}
                selected={selected}
                onSelect={setSelected}
                mobile={mobile}
                className="h-full"
              />
            </div>
            <div aria-hidden="true" className={`h-0.5 shrink-0 ${active ? 'animate-pulse bg-accent' : ''}`} />
          </div>
        </section>

        <aside className="order-2 flex shrink-0 flex-col border-t border-line bg-canvas md:order-1 md:min-h-0 md:w-[380px] md:border-r md:border-t-0">
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
              </div>
            ))}
            <div ref={end} />
          </div>
          {latest && !sheet && <div className="px-3 pt-2 md:hidden">{card(latest)}</div>}
          <div className="space-y-2 p-3">
            <ul className="-mx-3 flex gap-2 overflow-x-auto px-3 pb-1">
              {CHIPS.map((c) => (
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
                <textarea
                  ref={input}
                  aria-label="Ask for a change"
                  rows={2}
                  value={text}
                  placeholder="Ask for a change…"
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
                    disabled={!text.trim()}
                    className="rounded-md bg-accent p-2 text-accent-fg disabled:opacity-50"
                  >
                    <Send size={14} />
                  </button>
                )}
              </div>
            </form>
          </div>
        </aside>
      </div>
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
