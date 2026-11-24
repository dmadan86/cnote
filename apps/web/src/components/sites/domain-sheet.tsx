'use client'
import { useCallback, useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, ErrorText, Modal, Pill } from '@/components/ui'
import { checkHostname, type Site, type SiteDomain, type SiteDomainCheck, siteApi } from '@/lib/sites'
import { CopyButton } from './publish-modal'

// Menu paths only where a vendor page confirms them (docs/design/site-builder-phase2-facts.md); GoDaddy and BigRock
// were not verified, so they get the generic wording.
const REGISTRARS: { key: string; name: string; hint: string }[] = [
  { key: 'godaddy', name: 'GoDaddy', hint: "Open your domain's DNS settings and add each record." },
  { key: 'bigrock', name: 'BigRock', hint: "Open your domain's DNS settings and add each record." },
  {
    key: 'hostinger',
    name: 'Hostinger',
    hint: 'hPanel, Domains, DNS / Nameservers, Manage DNS records. Delete any existing www A or CNAME record first.',
  },
  {
    key: 'namecheap',
    name: 'Namecheap',
    hint: 'Domain List, Manage, Advanced DNS, Host Records, Add New Record. Enter only www as the Host, never the full name.',
  },
  {
    key: 'cloudflare',
    name: 'Cloudflare',
    hint: 'DNS, Records, Add record. Set Proxy status to "DNS only" (grey cloud) on both records.',
  },
  { key: 'other', name: 'Other', hint: "Open your domain's DNS settings and add each record." },
]
const TXT_NOTE = 'If your registrar adds your domain to the name by itself, type only the part before it.'
const clean = (v: string) =>
  v
    .trim()
    .toLowerCase()
    .replace(/^[a-z]+:\/\//, '')
    .replace(/[/?#].*$/, '')
    .replace(/\.$/, '')

/** Why a record is not right yet, in the owner's words: what we found against what we expected. */
function problem(r: SiteDomainCheck['records'][number]) {
  if (r.ok) return ''
  if (!r.found.length) return 'Not found yet.'
  if (r.found.length > 1)
    return `We found ${r.found.length} records here (${r.found.join(', ')}). Keep only the one below.`
  return `This record says ${r.found[0]}. It should be ${r.value}.`
}

/** Right-hand part of the domain connect flow, one card per hostname (design 17.1). Pro and admin only. */
export function DomainSheet({
  ws,
  site,
  domains,
  canAdmin,
  open,
  onClose,
  onChanged,
}: {
  ws: string
  site: Site
  domains: SiteDomain[]
  canAdmin: boolean
  open: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const [list, setList] = useState(domains)
  const [checks, setChecks] = useState<Record<string, SiteDomainCheck>>({})
  const [host, setHost] = useState('')
  const [registrar, setRegistrar] = useState('other')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const [drop, setDrop] = useState<string>()
  const listRef = useRef(list)
  listRef.current = list
  useEffect(() => setList(domains), [domains])

  const check = useCallback(
    async (names: string[]) => {
      const rs = await Promise.allSettled(names.map((n) => checkHostname(ws, site.id, n)))
      const got: Record<string, SiteDomainCheck> = {}
      rs.forEach((r, i) => {
        if (r.status === 'fulfilled') got[names[i]] = r.value
        else setErr(r.reason as Error)
      })
      setChecks((c) => ({ ...c, ...got }))
      setList((l) => l.map((d) => (got[d.hostname] ? { ...d, ...got[d.hostname].domain } : d)))
      if (Object.values(got).some((c) => c.verified)) onChanged()
    },
    [ws, site.id, onChanged],
  )
  // Check on open, then once a minute while anything is waiting (the server allows 120 checks an hour per site).
  useEffect(() => {
    if (!open) return
    const names = () => listRef.current.map((d) => d.hostname)
    if (names().length) check(names())
    const iv = setInterval(() => {
      const wait = listRef.current.filter((d) => d.status !== 'verified').map((d) => d.hostname)
      if (wait.length) check(wait)
    }, 60_000)
    return () => clearInterval(iv)
  }, [open, check])

  const run = async (fn: () => Promise<void>) => {
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
  const add = (name: string) =>
    run(async () => {
      const r = await siteApi.domainAdd(ws, site.id, name)
      setChecks((c) => ({ ...c, [r.domain.hostname]: r }))
      setList((l) => [...l.filter((d) => d.hostname !== r.domain.hostname), r.domain])
      setHost('')
      onChanged()
    })
  const remove = (name: string) =>
    run(async () => {
      await siteApi.domainRemove(ws, site.id, name)
      setList((l) => l.filter((d) => d.hostname !== name))
      setDrop(undefined)
      onChanged()
    })
  const hint = REGISTRARS.find((r) => r.key === registrar)?.hint
  const sub = site.url?.replace(/^https?:\/\//, '') ?? ''
  const typed = clean(host)
  // Offer the bare domain once www is there, because it redirects to www (design section 4, 17.1).
  const suggest = list.length === 1 && list[0].hostname.startsWith('www.') ? list[0].hostname.slice(4) : ''

  return (
    <Modal open={open} onClose={onClose} title="Use your own domain" className="max-w-xl">
      <div className="max-h-[70vh] space-y-4 overflow-y-auto text-[13px]">
        {!site.pro ? (
          <p>
            Your own domain is part of Website Pro. Your website keeps working at <b>{sub}</b>. Upgrade
            (coming soon).
          </p>
        ) : !site.subdomain || !site.published_version_id ? (
          <p>Publish your website first. Then you can add your own domain.</p>
        ) : (
          <>
            {canAdmin && (
              <form
                className="space-y-1"
                onSubmit={(e) => {
                  e.preventDefault()
                  if (typed) add(typed)
                }}
              >
                <label htmlFor="domain-host" className="text-[12px] font-medium text-muted">
                  Your domain
                </label>
                <div className="flex gap-2">
                  <input
                    id="domain-host"
                    value={host}
                    placeholder="www.sharmadental.in"
                    onChange={(e) => setHost(e.target.value)}
                    className="min-w-0 flex-1 rounded-md border border-line bg-canvas px-2.5 py-1.5 text-[14px]"
                  />
                  <button type="submit" className={btnPrimary} disabled={!typed || busy}>
                    Add
                  </button>
                </div>
                <p className="text-muted">
                  Type it without https://. Start with the www address. Don't have a domain? Buy one from any
                  registrar, then come back.
                </p>
              </form>
            )}
            {suggest && canAdmin && (
              <p className="rounded-md bg-hover px-2.5 py-1.5">
                Want <b>{suggest}</b> to work too? It then sends people to {list[0].hostname}.{' '}
                <button type="button" className="underline" disabled={busy} onClick={() => add(suggest)}>
                  Add {suggest}
                </button>
              </p>
            )}
            {list.length > 0 && (
              <div className="space-y-1">
                <p className="text-[12px] font-medium text-muted">Where did you buy it?</p>
                <div className="flex flex-wrap gap-1.5">
                  {REGISTRARS.map((r) => (
                    <button
                      key={r.key}
                      type="button"
                      aria-pressed={registrar === r.key}
                      className={`rounded-full border px-2.5 py-0.5 text-[12px] ${registrar === r.key ? 'border-accent bg-hover' : 'border-line'}`}
                      onClick={() => setRegistrar(r.key)}
                    >
                      {r.name}
                    </button>
                  ))}
                </div>
                <p className="text-muted">{hint}</p>
              </div>
            )}
            {list.map((d) => (
              <HostCard
                key={d.hostname}
                d={d}
                check={checks[d.hostname]}
                canAdmin={canAdmin}
                busy={busy}
                onCheck={() => check([d.hostname])}
                onDisconnect={() => setDrop(d.hostname)}
              />
            ))}
            {list.length > 0 && (
              <ul className="list-disc space-y-0.5 pl-4 text-muted">
                <li>Leave your email (MX) records as they are.</li>
                <li>Remove any old parking record for the same name before you add ours.</li>
                <li>
                  Most records show up within minutes; some registrars take up to 48 hours. You can close
                  this.
                </li>
                <li>https is set up for you after we find your records.</li>
              </ul>
            )}
          </>
        )}
        <p className="rounded-md border border-line p-2.5">
          <b>Your domain stays yours.</b> You keep it at your registrar; C Note never registers, transfers or
          locks it. {sub} keeps working, and you can disconnect any time.
        </p>
        <ErrorText error={err} />
        <div className="flex justify-end">
          <button type="button" className={btn} onClick={onClose}>
            Close
          </button>
        </div>
      </div>
      <Modal open={!!drop} onClose={() => setDrop(undefined)} title={`Disconnect ${drop}?`}>
        <div className="space-y-4 text-[13px]">
          <p>
            Your website goes back to {sub}. The domain stays yours at your registrar; nothing is deleted. You
            can connect it again any time.
          </p>
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setDrop(undefined)}>
              Cancel
            </button>
            <button
              type="button"
              disabled={busy}
              className="inline-flex items-center rounded-md bg-bad px-3 py-1.5 text-[13px] font-medium text-canvas disabled:opacity-50"
              onClick={() => drop && remove(drop)}
            >
              Disconnect
            </button>
          </div>
        </div>
      </Modal>
    </Modal>
  )
}

function HostCard({
  d,
  check,
  canAdmin,
  busy,
  onCheck,
  onDisconnect,
}: {
  d: SiteDomain
  check?: SiteDomainCheck
  canAdmin: boolean
  busy: boolean
  onCheck: () => void
  onDisconnect: () => void
}) {
  const tone = d.status === 'verified' ? 'ok' : d.status === 'lost' ? 'bad' : 'warn'
  const label = d.status === 'verified' ? 'Live' : d.status === 'lost' ? 'Lost' : 'Connecting'
  return (
    <section aria-label={d.hostname} className="space-y-2 rounded-lg border border-line p-3">
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate font-medium">{d.hostname}</span>
        <Pill tone={tone}>{label}</Pill>
      </div>
      {d.status === 'verified' && (
        <p className="text-ok">
          ✓ Live at{' '}
          <a className="underline" href={`https://${d.hostname}`} target="_blank" rel="noopener noreferrer">
            {d.hostname}
          </a>
        </p>
      )}
      {d.status === 'lost' && (
        <p role="alert" className="rounded-md bg-warn-bg px-2.5 py-1.5 text-warn">
          We can no longer see your records, so this address is not serving your website. Add them again, then
          check again.
        </p>
      )}
      {d.status === 'pending' && (
        <p className="text-muted">
          ○ Looking for your records… This can take a few minutes, sometimes up to 48 hours.
        </p>
      )}
      {check?.records.map((r, i) => (
        <div key={`${r.type}-${r.name}`} className="space-y-1 rounded-md bg-hover p-2.5">
          <div className="flex items-center gap-2">
            <b>
              Record {i + 1}:{' '}
              {r.type === 'TXT' ? 'proves the domain is yours' : 'sends visitors to your website'}
            </b>
            <span className={`ml-auto ${r.ok ? 'text-ok' : 'text-muted'}`}>
              {r.ok ? '✓ Correct' : '○ Not found yet'}
            </span>
          </div>
          {(
            [
              ['Type', r.type],
              ['Name / Host', r.name],
              ['Value / Points to', r.value],
            ] as const
          ).map(([k, v]) => (
            <div key={k} className="flex items-center gap-2">
              <span className="w-28 shrink-0 text-muted">{k}</span>
              <code className="min-w-0 flex-1 break-all">{v}</code>
              {k !== 'Type' && <CopyButton text={v} label="Copy" />}
            </div>
          ))}
          {r.type === 'TXT' && <p className="text-[12px] text-muted">{TXT_NOTE}</p>}
          {!r.ok && r.found.length > 0 && (
            <p role="alert" className="text-bad">
              {problem(r)}
            </p>
          )}
        </div>
      ))}
      <div className="flex items-center gap-2">
        <button type="button" className={btn} disabled={busy} onClick={onCheck}>
          Check again
        </button>
        {canAdmin && (
          <button type="button" className="ml-auto text-[12px] text-bad underline" onClick={onDisconnect}>
            Disconnect
          </button>
        )}
      </div>
    </section>
  )
}
