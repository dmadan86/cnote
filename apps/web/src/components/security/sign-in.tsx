'use client'
import { useState } from 'react'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { btn, btnPrimary, ErrorText, Field, inputCls } from '../ui'

type Domain = { domain: string; record: string; record_name: string; verified: boolean }

type Policy = {
  require_mfa_admins: boolean
  domains: Domain[]
  admins_without_mfa: { user_id: string; email: string }[]
  sso_callback_path: string
  sso_login_path: string
  sso: {
    enabled: boolean
    issuer: string
    client_id: string
    has_client_secret: boolean
    allowed_domains: string[]
    enforce: boolean
  }
}

/** Workspace sign-in policy (owners): require MFA for admins, OpenID Connect SSO. Admins can read it, only owners save. */
export function SignInSecurity({ ws }: { ws: string }) {
  const { data, reload, error: loadError } = useCmd<Policy>(ws, 'security.get')
  const [error, setError] = useState<Error>()
  const [msg, setMsg] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [replaceSecret, setReplaceSecret] = useState(false)
  if (loadError) return <ErrorText error={loadError} />
  if (!data) return null
  const callback = `${window.location.origin}${data.sso_callback_path}`

  async function save(name: string, input: unknown, done: string) {
    setBusy(true)
    setError(undefined)
    setMsg(undefined)
    try {
      await cmd(ws, name, input)
      setMsg(done)
      setReplaceSecret(false)
      reload()
    } catch (e) {
      setError(e as Error)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="max-w-xl space-y-8">
      <section className="space-y-3" aria-labelledby="mfa-policy">
        <h2 id="mfa-policy" className="text-[15px] font-semibold">
          Multi-factor authentication
        </h2>
        <label className="flex items-start gap-2 text-[14px]">
          <input
            type="checkbox"
            className="mt-1"
            checked={data.require_mfa_admins}
            disabled={busy}
            onChange={(e) =>
              save(
                'security.update',
                { require_mfa_admins: e.target.checked },
                e.target.checked
                  ? 'MFA is now required for admins and owners.'
                  : 'MFA is no longer required.',
              )
            }
          />
          <span>
            Require MFA for admins and owners
            <span className="block text-[12px] text-muted">
              You must have it on yourself first. Admins without it are held back at their next request until
              they set it up in Account security. Members and agents are not affected.
            </span>
          </span>
        </label>
        {data.admins_without_mfa.length > 0 && (
          <div className="rounded-md border border-line p-3 text-[13px]">
            <div className="font-medium">
              Admins and owners without MFA ({data.admins_without_mfa.length})
            </div>
            <ul className="mt-1 list-disc pl-5 text-muted">
              {data.admins_without_mfa.map((a) => (
                <li key={a.user_id} className="break-all">
                  {a.email}
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>

      <section className="space-y-3" aria-labelledby="sso-domains">
        <h2 id="sso-domains" className="text-[15px] font-semibold">
          SSO domains
        </h2>
        <p className="text-[12px] text-muted">
          Single sign-on only applies to email domains you have proven you control. Add a domain, publish the
          TXT record at your DNS host, then verify.
        </p>
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            const form = e.currentTarget
            const domain = String(new FormData(form).get('domain') ?? '').trim()
            save(
              'security.domain_add',
              { domain },
              'Domain added. Publish the TXT record, then verify.',
            ).then(() => form.reset())
          }}
        >
          <input
            aria-label="Domain to add"
            name="domain"
            required
            placeholder="acme.com"
            className={inputCls}
          />
          <button type="submit" disabled={busy} className={btn}>
            Add domain
          </button>
        </form>
        <ul className="space-y-2">
          {data.domains.map((d) => (
            <li key={d.domain} className="space-y-1 rounded-md border border-line p-3 text-[13px]">
              <div className="flex flex-wrap items-center gap-2">
                <span className="break-all font-medium">{d.domain}</span>
                <span className="rounded bg-side px-1.5 py-0.5 text-[12px]">
                  {d.verified ? 'Verified' : 'Not verified'}
                </span>
                {!d.verified && (
                  <button
                    type="button"
                    disabled={busy}
                    className={btn}
                    onClick={() =>
                      save('security.domain_verify', { domain: d.domain }, `${d.domain} verified.`)
                    }
                  >
                    Verify {d.domain}
                  </button>
                )}
                <button
                  type="button"
                  disabled={busy}
                  className={btn}
                  onClick={() => save('security.domain_remove', { domain: d.domain }, `${d.domain} removed.`)}
                >
                  Remove {d.domain}
                </button>
              </div>
              {!d.verified && (
                <div className="text-muted">
                  Add a TXT record at <code className="break-all">{d.domain}</code> or{' '}
                  <code className="break-all">{d.record_name}</code> with the value{' '}
                  <code data-testid="txt-record" className="break-all">
                    {d.record}
                  </code>
                </div>
              )}
            </li>
          ))}
        </ul>
      </section>

      <form
        className="space-y-3"
        aria-labelledby="sso-title"
        onSubmit={(e) => {
          e.preventDefault()
          const f = new FormData(e.currentTarget)
          const g = (k: string) => String(f.get(k) ?? '').trim()
          const sso: Record<string, unknown> = {
            enabled: f.get('enabled') === 'on',
            issuer: g('issuer'),
            client_id: g('client_id'),
            allowed_domains: g('domains')
              .split(/[\s,]+/)
              .filter(Boolean),
            enforce: f.get('enforce') === 'on',
          }
          if (g('client_secret')) sso.client_secret = g('client_secret')
          save('security.update', { sso }, 'Single sign-on settings saved.')
        }}
      >
        <h2 id="sso-title" className="text-[15px] font-semibold">
          Single sign-on (OpenID Connect)
        </h2>
        <p className="text-[12px] text-muted">
          Works with Google Workspace, Microsoft Entra, Okta and other OpenID Connect providers. SAML is not
          supported yet.
        </p>
        <Field label="Redirect (callback) URL to register with your provider">
          {(id) => (
            <div className="flex gap-2">
              <input id={id} readOnly value={callback} className={inputCls} />
              <button
                type="button"
                className={btn}
                onClick={() =>
                  navigator.clipboard.writeText(callback).then(() => setMsg('Callback URL copied.'))
                }
              >
                Copy
              </button>
            </div>
          )}
        </Field>
        <Field label="Issuer URL">
          {(id) => (
            <input
              id={id}
              name="issuer"
              defaultValue={data.sso.issuer}
              placeholder="https://accounts.example.com"
              className={inputCls}
            />
          )}
        </Field>
        <Field label="Client ID">
          {(id) => (
            <input
              id={id}
              name="client_id"
              defaultValue={data.sso.client_id}
              autoComplete="off"
              className={inputCls}
            />
          )}
        </Field>
        <Field label="Client secret">
          {(id) =>
            data.sso.has_client_secret && !replaceSecret ? (
              <div className="flex items-center gap-2 text-[13px]">
                <span id={id}>Secret configured</span>
                <button type="button" className={btn} onClick={() => setReplaceSecret(true)}>
                  Replace
                </button>
              </div>
            ) : (
              <input
                id={id}
                name="client_secret"
                type="password"
                autoComplete="new-password"
                className={inputCls}
              />
            )
          }
        </Field>
        <label className="flex items-center gap-2 text-[14px]">
          <input type="checkbox" name="enabled" defaultChecked={data.sso.enabled} /> Enable single sign-on
        </label>
        <label className="flex items-start gap-2 text-[14px]">
          <input type="checkbox" name="enforce" defaultChecked={data.sso.enforce} className="mt-1" />
          <span>
            Require SSO for people on verified domains
            <span className="block text-[12px] text-muted">
              Blocks password sign-in for them. Owners keep password sign-in so a provider outage cannot lock
              you out. New people who sign in this way join as members. Sign-in address:{' '}
              <code className="break-all">{data.sso_login_path}</code>
            </span>
          </span>
        </label>
        <ErrorText error={error} />
        <div role="status" className="text-[13px] text-muted">
          {msg}
        </div>
        <button type="submit" disabled={busy} className={btnPrimary}>
          Save single sign-on
        </button>
      </form>
    </div>
  )
}
