'use client'
import { useCallback, useEffect, useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls, Modal, Pill } from '@/components/ui'
import {
  type EntryStatus,
  entrySource,
  entryStatus,
  type LangEntry,
  type LangReview,
  langLimit,
  langName,
  langNative,
  type Site,
  type SiteData,
  type SiteLangInfo,
  SUPPORTED_LANGS,
  setLangHidden,
  siteApi,
  siteApi3b,
} from '@/lib/sites'

type Info = SiteLangInfo & { hidden?: boolean }
const TONE: Record<EntryStatus, 'ok' | 'warn' | 'bad'> = { ok: 'ok', 'needs-update': 'warn', missing: 'bad' }
const LABEL: Record<EntryStatus, string> = { ok: 'OK', 'needs-update': 'Needs update', missing: 'Missing' }
const keyOf = (page: string, e: LangEntry) => `${page}|${e.block_id}|${e.prop}|${e.index ?? ''}`

/**
 * Languages section of the website settings (design 18): add or remove a language within the plan, hide it until it is
 * checked, translate the site (one chat turn of kind 'translate', polled like any turn) and review every entry.
 * Renders nothing when the server has the languages switch off (site.lang_list fails).
 */
export function LanguagesPanel({
  ws,
  site,
  data,
  canAdmin,
  onReload,
}: {
  ws: string
  site: Site
  data: SiteData
  canAdmin: boolean
  onReload: () => void
}) {
  const [langs, setLangs] = useState<Info[] | null>(null)
  const [off, setOff] = useState(false)
  const [add, setAdd] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const [running, setRunning] = useState<{ turn: string; lang: string; steps: number } | null>(null)
  const [reviewing, setReviewing] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      setLangs((await siteApi3b.langList(ws, site.id)).langs)
    } catch {
      setOff(true)
    }
  }, [ws, site.id])
  useEffect(() => {
    refresh()
  }, [refresh])

  // The translate turn: poll once a second; when it ends, reload the languages and the draft.
  const turnId = running?.turn
  useEffect(() => {
    if (!turnId) return
    const iv = setInterval(async () => {
      try {
        const { turn } = await siteApi.turn(ws, turnId)
        if (turn.status === 'queued' || turn.status === 'running') {
          setRunning((r) => (r ? { ...r, steps: turn.steps?.length ?? 0 } : r))
          return
        }
        clearInterval(iv)
        setRunning(null)
        if (turn.status === 'failed') setErr(new Error(turn.error ?? 'The translation did not finish.'))
        await refresh()
        onReload()
      } catch (e) {
        clearInterval(iv)
        setRunning(null)
        setErr(e as Error)
      }
    }, 1000)
    return () => clearInterval(iv)
  }, [turnId, ws, refresh, onReload])

  if (off || !langs) return null
  const limit = langLimit(site.pro)
  const taken = new Set(langs.map((l) => l.code))
  const options = SUPPORTED_LANGS.filter((l) => l.code !== 'en' && !l.extra && !taken.has(l.code))
  const full = langs.length >= limit
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setErr(undefined)
    try {
      await fn()
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const translate = (lang: string) =>
    run(async () => {
      const r = await siteApi3b.chatTranslate(ws, site.id, site.draft_version_id, lang)
      setRunning({ turn: r.turn_id, lang, steps: 0 })
    })

  return (
    <section className="space-y-2">
      <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">Languages</h3>
      <p className="text-muted">
        Visitors get each language at its own address (for example /hi/). English is always there. Your plan
        includes {limit} more {limit === 1 ? 'language' : 'languages'}.
      </p>
      {langs.map((l) => {
        const live = running?.lang === l.code
        return (
          <div key={l.code} className="space-y-1.5 rounded-md border border-line p-2.5">
            <div className="flex flex-wrap items-center gap-2">
              <span className="min-w-0 flex-1 font-medium">
                {langNative(l.code)} <span className="font-normal text-muted">{langName(l.code)}</span>
              </span>
              {l.hidden && <Pill tone="neutral">Hidden</Pill>}
              {l.stale + l.missing > 0 && <Pill tone="warn">{l.stale + l.missing} to do</Pill>}
            </div>
            <p className="text-muted">
              {l.translated} translated · {l.stale} need an update · {l.missing} missing · {l.unchecked} not
              checked yet
            </p>
            {live && (
              <p role="status" className="text-muted">
                Translating{running?.steps ? `, ${running.steps} steps done` : ''}…
              </p>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                className={btnPrimary}
                disabled={busy || !!running || !canAdmin}
                onClick={() => translate(l.code)}
              >
                Translate site
              </button>
              <button type="button" className={btn} onClick={() => setReviewing(l.code)}>
                Review
              </button>
              <button
                type="button"
                className={btn}
                disabled={busy || !canAdmin}
                onClick={() =>
                  run(async () => {
                    setLangs((await setLangHidden(ws, site.id, l.code, !l.hidden)).langs)
                    onReload()
                  })
                }
              >
                {l.hidden ? 'Show to visitors' : 'Hide until checked'}
              </button>
              <button
                type="button"
                className={btn}
                disabled={busy || !!running || !canAdmin}
                onClick={() =>
                  window.confirm(`Remove ${langName(l.code)}? Its translations are deleted.`) &&
                  run(async () => {
                    setLangs((await siteApi3b.langRemove(ws, site.id, l.code)).langs)
                    onReload()
                  })
                }
              >
                Remove
              </button>
            </div>
          </div>
        )
      })}
      <div className="flex items-center gap-2">
        <select
          aria-label="Language to add"
          className={inputCls}
          value={add}
          disabled={full || busy || !canAdmin}
          onChange={(e) => setAdd(e.target.value)}
        >
          <option value="">{full ? 'Language limit reached' : 'Add a language…'}</option>
          {options.map((o) => (
            <option key={o.code} value={o.code}>
              {o.native} ({o.name})
            </option>
          ))}
        </select>
        <button
          type="button"
          className={btn}
          disabled={!add || full || busy || !canAdmin}
          onClick={() =>
            run(async () => {
              setLangs((await siteApi3b.langAdd(ws, site.id, add)).langs)
              onReload() // each change is a new draft version
              setAdd('')
            })
          }
        >
          Add
        </button>
      </div>
      {full && !site.pro && <p className="text-muted">Website Pro includes 3 more languages.</p>}
      <p className="text-muted">
        Translations are machine-made. Read them before you show a language; a changed English line shows in
        English until it is translated again.
      </p>
      <ErrorText error={err} />
      {reviewing && (
        <ReviewSheet
          ws={ws}
          site={site}
          data={data}
          lang={reviewing}
          onClose={() => setReviewing(null)}
          onChanged={() => {
            refresh()
            onReload()
          }}
        />
      )}
    </section>
  )
}

/** Every entry of one language, per page: source, translation, status. Edit one entry; mark a page or all as checked. */
function ReviewSheet({
  ws,
  site,
  data,
  lang,
  onClose,
  onChanged,
}: {
  ws: string
  site: Site
  data: SiteData
  lang: string
  onClose: () => void
  onChanged: () => void
}) {
  const [rev, setRev] = useState<LangReview | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const load = useCallback(async () => {
    try {
      setRev(await siteApi3b.langReview(ws, site.id, lang))
    } catch (e) {
      setErr(e as Error)
    }
  }, [ws, site.id, lang])
  useEffect(() => {
    load()
  }, [load])
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setErr(undefined)
    try {
      await fn()
      await load()
      onChanged()
    } catch (e) {
      setErr(e as Error)
    } finally {
      setBusy(false)
    }
  }
  const title = (slug: string) => (slug ? (data.pages?.find((p) => p.slug === slug)?.title ?? slug) : 'Home')

  return (
    <Modal open onClose={onClose} title={`Review ${langName(lang)}`} className="max-w-3xl">
      {/* Esc closes this sheet only, not the settings panel behind it */}
      {/* biome-ignore lint/a11y/noStaticElementInteractions: a key filter, not a control */}
      <div onKeyDown={(e) => e.key === 'Escape' && e.stopPropagation()}>
        <div className="max-h-[70vh] space-y-5 overflow-y-auto text-[13px]">
          {!rev && !err && <p className="text-muted">Loading…</p>}
          {rev?.pages.length === 0 && <p className="text-muted">Nothing to translate yet.</p>}
          {rev?.pages.map((pg) => (
            <section key={pg.slug || 'home'} className="space-y-2">
              <div className="flex items-center gap-2">
                <h3 className="flex-1 text-[14px] font-semibold">{title(pg.slug)}</h3>
                <button
                  type="button"
                  className={btn}
                  disabled={busy}
                  onClick={() => run(() => siteApi3b.langMarkChecked(ws, site.id, lang, pg.slug))}
                >
                  Mark page as checked
                </button>
              </div>
              <ul className="divide-y divide-line rounded-md border border-line">
                {pg.entries.map((e) => {
                  const k = keyOf(pg.slug, e)
                  const st = entryStatus(e)
                  return (
                    <li key={k} className="grid gap-2 p-2.5 md:grid-cols-[1fr_1fr_auto]">
                      <p className="whitespace-pre-line text-muted">
                        <span className="sr-only">Source: </span>
                        {entrySource(data, pg.slug, e)}
                      </p>
                      {editing === k ? (
                        <div className="space-y-1.5">
                          <textarea
                            aria-label={`Translation, ${langName(lang)}`}
                            lang={lang}
                            rows={3}
                            value={text}
                            onChange={(ev) => setText(ev.target.value)}
                            className={inputCls}
                          />
                          <div className="flex gap-2">
                            <button
                              type="button"
                              className={btnPrimary}
                              disabled={busy || !text.trim()}
                              onClick={() =>
                                run(async () => {
                                  await siteApi3b.langEntrySet(ws, site.id, site.draft_version_id, lang, {
                                    page: pg.slug,
                                    block_id: e.block_id,
                                    prop: e.prop,
                                    index: e.index,
                                    text: text.trim(),
                                  })
                                  setEditing(null)
                                })
                              }
                            >
                              Save
                            </button>
                            <button type="button" className={btn} onClick={() => setEditing(null)}>
                              Cancel
                            </button>
                          </div>
                        </div>
                      ) : (
                        <p lang={lang} className="whitespace-pre-line">
                          <span className="sr-only">Translation: </span>
                          {e.text || <span className="text-muted">Not translated yet</span>}
                        </p>
                      )}
                      <div className="flex items-start gap-2 md:flex-col">
                        <Pill tone={TONE[st]}>{LABEL[st]}</Pill>
                        {st === 'ok' && !e.checked && (
                          <span className="text-[12px] text-muted">Not checked</span>
                        )}
                        {editing !== k && (
                          <button
                            type="button"
                            className={btn}
                            disabled={busy}
                            onClick={() => {
                              setEditing(k)
                              setText(e.text)
                            }}
                          >
                            Edit
                          </button>
                        )}
                      </div>
                    </li>
                  )
                })}
              </ul>
            </section>
          ))}
          <ErrorText error={err} />
        </div>
        <div className="mt-3 flex justify-end gap-2">
          <button
            type="button"
            className={btn}
            disabled={busy || !rev?.pages.length}
            onClick={() => run(() => siteApi3b.langMarkChecked(ws, site.id, lang))}
          >
            Mark all as checked
          </button>
          <button type="button" className={btnPrimary} onClick={onClose}>
            Done
          </button>
        </div>
      </div>
    </Modal>
  )
}
