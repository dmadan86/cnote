'use client'
import { useState } from 'react'
import { useShell } from '@/components/shell'
import { btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { cmd } from '@/lib/api'

const SOURCES = ['jira', 'confluence', 'notion', 'trello', 'csv'] as const
const MAP_FIELDS = [
  ['title', 'Title (required)'],
  ['id', 'Row id'],
  ['description', 'Description'],
  ['status', 'Status'],
  ['priority', 'Priority'],
  ['item_type', 'Item type'],
  ['assignee_email', 'Assignee email'],
  ['labels', 'Labels'],
  ['parent', 'Parent row id'],
  ['url', 'URL'],
] as const

/** First CSV line -> column names (handles quoted commas; header cells with embedded newlines are not supported). */
function csvHeaders(text: string) {
  const line = text.split(/\r?\n/, 1)[0] ?? ''
  const out: string[] = []
  let cur = ''
  let q = false
  for (const ch of line) {
    if (ch === '"') q = !q
    else if (ch === ',' && !q) {
      out.push(cur.trim())
      cur = ''
    } else cur += ch
  }
  out.push(cur.trim())
  return out.filter(Boolean)
}

export function ImportForm({ ws, onStarted }: { ws: string; onStarted: () => void }) {
  const { spaces } = useShell()
  const [source, setSource] = useState<(typeof SOURCES)[number]>('csv')
  const [live, setLive] = useState(false)
  const [content, setContent] = useState('')
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  const [busy, setBusy] = useState(false)
  const cols = source === 'csv' ? csvHeaders(content) : []

  async function submit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = e.currentTarget
    const f = new FormData(form)
    const body: Record<string, unknown> = { source, space_id: f.get('space') }
    if (source === 'jira' && live) {
      body.jira = {
        base_url: f.get('base_url'),
        email: f.get('email'),
        api_token: f.get('api_token'),
        jql: f.get('jql'),
      }
    } else body.content = content
    if (source === 'csv') {
      const m: Record<string, string> = {}
      for (const [k] of MAP_FIELDS) if (f.get(`map_${k}`)) m[k] = String(f.get(`map_${k}`))
      body.mapping = { ...m, target: f.get('target') }
    }
    setBusy(true)
    try {
      const r = await cmd<{ records: number }>(ws, 'import.start', body)
      setMsg(`Import queued: ${r.records} record${r.records === 1 ? '' : 's'}.`)
      setErr(undefined)
      setContent('')
      form.reset() // the Jira token only ever lived in this uncontrolled field
      onStarted()
    } catch (x) {
      setMsg(undefined)
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      onSubmit={submit}
      className="space-y-4 rounded-lg border border-line p-4"
      aria-label="Start an import"
    >
      <div className="grid grid-cols-2 gap-3">
        <Field label="Source">
          {(id) => (
            <select
              id={id}
              value={source}
              onChange={(e) => setSource(e.target.value as typeof source)}
              className={inputCls}
            >
              {SOURCES.map((s) => (
                <option key={s}>{s}</option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Target space">
          {(id) => (
            <select id={id} name="space" required className={inputCls}>
              {spaces.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          )}
        </Field>
      </div>

      {source === 'jira' && (
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} /> Fetch live from
          Jira Cloud
        </label>
      )}

      {source === 'jira' && live ? (
        <div className="grid grid-cols-2 gap-3">
          <Field label="Jira base URL">
            {(id) => (
              <input
                id={id}
                name="base_url"
                required
                type="url"
                placeholder="https://acme.atlassian.net"
                pattern="https://[^/]+\.atlassian\.net/?"
                className={inputCls}
              />
            )}
          </Field>
          <Field label="JQL">
            {(id) => <input id={id} name="jql" required placeholder="project = ENG" className={inputCls} />}
          </Field>
          <Field label="Atlassian email">
            {(id) => (
              <input id={id} name="email" required type="email" autoComplete="off" className={inputCls} />
            )}
          </Field>
          <Field label="API token (sent once, never stored)">
            {(id) => (
              <input
                id={id}
                name="api_token"
                required
                type="password"
                autoComplete="off"
                className={inputCls}
              />
            )}
          </Field>
        </div>
      ) : (
        <>
          <Field label="Upload a file (read in your browser)">
            {(id) => (
              <input
                id={id}
                type="file"
                onChange={async (e) => {
                  const file = e.target.files?.[0]
                  if (file) setContent(await file.text())
                }}
                className="block text-[13px]"
              />
            )}
          </Field>
          <Field label="Or paste the export">
            {(id) => (
              <textarea
                id={id}
                rows={6}
                required
                value={content}
                onChange={(e) => setContent(e.target.value)}
                className={`${inputCls} font-mono text-[12px]`}
              />
            )}
          </Field>
        </>
      )}

      {source === 'csv' && (
        <fieldset className="space-y-2">
          <legend className="text-[12px] font-medium text-muted">Column mapping</legend>
          {cols.length === 0 && <p className="text-[13px] text-muted">Add CSV content to pick columns.</p>}
          {cols.length > 0 && (
            <div className="grid grid-cols-2 gap-3">
              <Field label="Import as">
                {(id) => (
                  <select id={id} name="target" className={inputCls}>
                    <option value="items">Items</option>
                    <option value="pages">Pages</option>
                  </select>
                )}
              </Field>
              {MAP_FIELDS.map(([k, label]) => (
                <Field key={`${k}-${cols.join()}`} label={label}>
                  {(id) => (
                    <select
                      id={id}
                      name={`map_${k}`}
                      required={k === 'title'}
                      defaultValue={cols.find((c) => c.toLowerCase().replace(/\W/g, '_') === k) ?? ''}
                      className={inputCls}
                    >
                      <option value="">{k === 'title' ? 'Choose column...' : 'None'}</option>
                      {cols.map((c) => (
                        <option key={c}>{c}</option>
                      ))}
                    </select>
                  )}
                </Field>
              ))}
            </div>
          )}
        </fieldset>
      )}

      <ErrorText error={err} />
      {msg && (
        <p role="status" className="text-[13px] text-ok">
          {msg}
        </p>
      )}
      <button type="submit" disabled={busy} className={btnPrimary}>
        {busy ? 'Starting...' : 'Start import'}
      </button>
    </form>
  )
}
