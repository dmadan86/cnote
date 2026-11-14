'use client'
import { useState } from 'react'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal, Pill } from '@/components/ui'
import { cmd } from '@/lib/api'
import { ago, useCmd } from '@/lib/hooks'
import { download } from './log'

type Contact = { id: string; name: string; email: string | null }
type Retention = { years: number; events_older: number; oldest_event: string | null }
type Request = {
  id: string
  kind: 'export' | 'erase'
  status: 'pending' | 'done' | 'refused'
  subject_name: string | null
  reason: string | null
  result: { mode?: string; kept?: string[]; unpaid_documents?: number }
  created_at: string
  requested_by_name: string | null
}
const tone = { done: 'ok', refused: 'warn', pending: 'neutral' } as const

function Retention({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Retention>(ws, 'audit.retention')
  const [msg, setMsg] = useState('')
  if (!data) return null
  return (
    <section className="max-w-xl space-y-2">
      <h2 className="text-[14px] font-semibold">Retention</h2>
      <form
        className="flex items-end gap-2"
        onSubmit={async (e) => {
          e.preventDefault()
          try {
            await cmd(ws, 'audit.retention', { years: Number(new FormData(e.currentTarget).get('years')) })
            setMsg('Saved')
            reload()
          } catch (x) {
            setMsg((x as Error).message)
          }
        }}
      >
        <Field label="Keep records for (years)">
          {(id) => (
            <input
              id={id}
              name="years"
              type="number"
              min={1}
              max={30}
              defaultValue={data.years}
              className={inputCls}
            />
          )}
        </Field>
        <button type="submit" className={btn}>
          Save
        </button>
      </form>
      <p className="text-[13px] text-muted">
        Used for reporting only; changing it does not delete anything. {data.events_older.toLocaleString()}{' '}
        audit events are older than {data.years} years. GST law asks for records for 72 months after the
        annual return due date and the Companies Act for 8 years; confirm the period with your CA.
      </p>
      <p role="status" className="text-[13px]">
        {msg}
      </p>
    </section>
  )
}

function EraseModal({
  ws,
  c,
  onClose,
  onDone,
}: {
  ws: string
  c: Contact
  onClose: () => void
  onDone: () => void
}) {
  const [typed, setTyped] = useState('')
  const [reason, setReason] = useState('')
  const [unpaid, setUnpaid] = useState<number>()
  const [force, setForce] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  const ready = typed === c.name && reason.trim() !== '' && (unpaid === undefined || force)
  const run = async () => {
    setBusy(true)
    try {
      const r = await cmd<{ status: string; unpaid_documents?: number; mode?: string }>(
        ws,
        'data.erase_subject',
        {
          contact_id: c.id,
          reason,
          confirm_unpaid: force || undefined,
        },
      )
      if (r.status === 'refused') {
        setUnpaid(r.unpaid_documents)
        setBusy(false)
        return
      }
      onDone()
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }
  return (
    <Modal open onClose={onClose} title={`Erase ${c.name}`} className="max-w-lg">
      <div className="max-h-[70vh] space-y-3 overflow-y-auto text-[13px]">
        <div>
          <p className="font-medium">Anonymised</p>
          <p className="text-muted">
            Email, phone, company, consent record, notes text, activity and comment text, and the search index
            of this contact.
          </p>
        </div>
        <div>
          <p className="font-medium">Kept</p>
          <p className="text-muted">
            Invoices, receipts, bills and the ledger stay. If this contact has issued documents, its name,
            GSTIN, state and address stay too because they are printed on them (GST: 72 months after the
            annual return due date; Companies Act: 8 years; confirm with your CA). The audit log and page
            versions are append-only and stay.
          </p>
        </div>
        {unpaid !== undefined && (
          <label className="flex items-start gap-2 rounded border border-warn p-2">
            <input
              type="checkbox"
              className="mt-1"
              checked={force}
              onChange={(e) => setForce(e.target.checked)}
            />
            <span>
              {unpaid} invoice(s) or bill(s) are still unpaid. I understand and want to erase anyway.
            </span>
          </label>
        )}
        <Field label="Reason (kept in the request history)">
          {(id) => (
            <input id={id} className={inputCls} value={reason} onChange={(e) => setReason(e.target.value)} />
          )}
        </Field>
        <Field label={`Type the name "${c.name}" to confirm`}>
          {(id) => (
            <input
              id={id}
              className={inputCls}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoComplete="off"
            />
          )}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="button" className={btnPrimary} disabled={!ready || busy} onClick={run}>
            Anonymise contact
          </button>
        </div>
      </div>
    </Modal>
  )
}

function Subject({ ws, done }: { ws: string; done: () => void }) {
  const { data } = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const [q, setQ] = useState('')
  const [pick, setPick] = useState<Contact>()
  const [erase, setErase] = useState(false)
  const [msg, setMsg] = useState<string>()
  const hits = q.trim()
    ? (data ?? [])
        .filter((c) => `${c.name} ${c.email ?? ''}`.toLowerCase().includes(q.toLowerCase()))
        .slice(0, 8)
    : []
  return (
    <section className="max-w-xl space-y-2">
      <h2 className="text-[14px] font-semibold">A person's data</h2>
      <Field label="Find a contact">
        {(id) => (
          <input
            id={id}
            className={inputCls}
            value={q}
            placeholder="Name or email"
            onChange={(e) => {
              setQ(e.target.value)
              setPick(undefined)
              setMsg(undefined)
            }}
          />
        )}
      </Field>
      <ul
        aria-label="Matching contacts"
        className="divide-y divide-line rounded-md border border-line empty:hidden"
      >
        {hits.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              className="w-full px-3 py-1.5 text-left hover:bg-hover"
              onClick={() => setPick(c)}
            >
              {c.name} <span className="text-muted">{c.email ?? c.id.slice(0, 8)}</span>
            </button>
          </li>
        ))}
      </ul>
      {pick && (
        <div className="space-y-2 rounded-md border border-line p-3">
          <p>
            Selected: <strong>{pick.name}</strong> <span className="text-muted">{pick.email}</span>
          </p>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className={btn}
              onClick={async () => {
                try {
                  const r = await cmd<{ data?: unknown }>(ws, 'data.export_subject', { contact_id: pick.id })
                  if (r.data)
                    download(`${pick.name}-data.json`, JSON.stringify(r.data, null, 2), 'application/json')
                  setMsg('Export downloaded and logged')
                  done()
                } catch (e) {
                  setMsg((e as Error).message)
                }
              }}
            >
              Export their data (JSON)
            </button>
            <button type="button" className={`${btn} border-bad text-bad`} onClick={() => setErase(true)}>
              Erase...
            </button>
          </div>
        </div>
      )}
      <p role="status" className="text-[13px]">
        {msg}
      </p>
      {erase && pick && (
        <EraseModal
          ws={ws}
          c={pick}
          onClose={() => setErase(false)}
          onDone={() => {
            setErase(false)
            setPick(undefined)
            setQ('')
            setMsg('Contact anonymised')
            done()
          }}
        />
      )}
    </section>
  )
}

export function Privacy({ ws }: { ws: string }) {
  const { data, reload } = useCmd<Request[]>(ws, 'data.request_list')
  return (
    <div className="space-y-8">
      <Retention ws={ws} />
      <Subject ws={ws} done={reload} />
      <section className="space-y-2">
        <h2 className="text-[14px] font-semibold">Requests</h2>
        {data?.length === 0 && <p className="text-muted">No export or erase requests yet.</p>}
        <ul className="divide-y divide-line rounded-md border border-line">
          {data?.map((r) => (
            <li key={r.id} className="space-y-0.5 px-3 py-2 text-[13px]">
              <span className="font-medium">{r.kind === 'erase' ? 'Erase' : 'Export'}</span>{' '}
              {r.subject_name ?? 'removed contact'} <Pill tone={tone[r.status]}>{r.status}</Pill>
              <span className="text-muted">
                {' '}
                {ago(r.created_at)} by {r.requested_by_name ?? 'someone'}
                {r.result.mode ? `, ${r.result.mode} erase` : ''}
                {r.reason ? `: ${r.reason}` : ''}
              </span>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}
