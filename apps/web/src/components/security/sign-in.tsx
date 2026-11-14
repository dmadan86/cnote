'use client'
import { useState } from 'react'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { useShell } from '../shell'
import { btn, btnPrimary, ErrorText, Field, inputCls } from '../ui'

type Domain = { domain: string; record: string; record_name: string; verified: boolean }

type Policy = {
  require_mfa_admins: boolean
  domains: Domain[]
  admins_without_mfa: { user_id: string; email: string }[]
  sso_callback_path: string
  sso_login_path: string
  saml_metadata_path: string
  saml_acs_path: string
  sso: {
    enabled: boolean
    protocol: 'oidc' | 'saml'
    issuer: string
    client_id: string
    has_client_secret: boolean
    enforce: boolean
    saml: { has_metadata: boolean; metadata_url: string; idp_entity_id: string; email_attribute: string }
  }
}

/** Workspace sign-in policy. Admins can read it; only owners can change it, so members see a note and admins see no controls. */
export function SignInSecurity({ ws }: { ws: string }) {
  const role = useShell().me.workspaces.find((w) => w.id === ws)?.role
  if (role !== 'owner' && role !== 'admin')
    return (
      <p className="text-[13px] text-muted">Only workspace owners and admins can see sign-in security.</p>
    )
  return <Policy ws={ws} owner={role === 'owner'} />
}

function Policy({ ws, owner }: { ws: string; owner: boolean }) {
  const { data, reload, error: loadError } = useCmd<Policy>(ws, 'security.get')
  const [protocol, setProtocol] = useState<'oidc' | 'saml' | undefined>()
  const [samlMode, setSamlMode] = useState<'url' | 'xml'>('url')
  const [error, setError] = useState<Error>()
  const [msg, setMsg] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [replaceSecret, setReplaceSecret] = useState(false)
  if (loadError) return <ErrorText error={loadError} />
  if (!data) return null
  const proto = protocol ?? data.sso.protocol
  const callback = `${window.location.origin}${data.sso_callback_path}`
  const spMetadata = `${window.location.origin}${data.saml_metadata_path}`
  const acs = `${window.location.origin}${data.saml_acs_path}`
  const copy = (text: string, what: string) =>
    navigator.clipboard.writeText(text).then(() => setMsg(`${what} copied.`))

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
        {!owner && (
          <p className="text-[13px]">
            MFA is {data.require_mfa_admins ? 'required' : 'not required'} for admins and owners. Only
            workspace owners can change this.
          </p>
        )}
        {owner && (
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
                You must have it on yourself first. Admins without it are held back at their next request
                until they set it up in Account security. Members and agents are not affected.
              </span>
            </span>
          </label>
        )}
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
        {owner && (
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
        )}
        <ul className="space-y-2">
          {data.domains.map((d) => (
            <li key={d.domain} className="space-y-1 rounded-md border border-line p-3 text-[13px]">
              <div className="flex flex-wrap items-center gap-2">
                <span className="break-all font-medium">{d.domain}</span>
                <span className="rounded bg-side px-1.5 py-0.5 text-[12px]">
                  {d.verified ? 'Verified' : 'Not verified'}
                </span>
                {owner && !d.verified && (
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
                {owner && (
                  <button
                    type="button"
                    disabled={busy}
                    className={btn}
                    onClick={() =>
                      save('security.domain_remove', { domain: d.domain }, `${d.domain} removed.`)
                    }
                  >
                    Remove {d.domain}
                  </button>
                )}
              </div>
              {owner && !d.verified && (
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

      {!owner ? (
        <section className="space-y-2" aria-labelledby="sso-title">
          <h2 id="sso-title" className="text-[15px] font-semibold">
            Single sign-on
          </h2>
          <p className="text-[13px]">
            {data.sso.enabled
              ? `On (${data.sso.protocol === 'saml' ? 'SAML 2.0' : 'OpenID Connect'})`
              : 'Off'}
            {data.sso.enabled && data.sso.enforce ? ', required for people on verified domains' : ''}. Only
            workspace owners can change these settings.
          </p>
        </section>
      ) : (
        <form
          className="space-y-3"
          aria-labelledby="sso-title"
          onSubmit={(e) => {
            e.preventDefault()
            const f = new FormData(e.currentTarget)
            const g = (k: string) => String(f.get(k) ?? '').trim()
            const sso: Record<string, unknown> = {
              enabled: f.get('enabled') === 'on',
              protocol: proto,
              enforce: f.get('enforce') === 'on',
            }
            if (proto === 'oidc') {
              sso.issuer = g('issuer')
              sso.client_id = g('client_id')
              if (g('client_secret')) sso.client_secret = g('client_secret')
            } else {
              sso.saml_email_attribute = g('saml_email_attribute')
              if (samlMode === 'url') sso.saml_metadata_url = g('saml_metadata_url')
              else sso.saml_metadata = g('saml_metadata')
            }
            save('security.update', { sso }, 'Single sign-on settings saved.')
          }}
        >
          <h2 id="sso-title" className="text-[15px] font-semibold">
            Single sign-on
          </h2>
          <fieldset className="flex flex-wrap gap-4 text-[14px]">
            <legend className="mb-1 text-[12px] font-medium text-muted">Protocol</legend>
            {(['oidc', 'saml'] as const).map((p) => (
              <label key={p} className="flex items-center gap-2">
                <input type="radio" name="protocol" checked={proto === p} onChange={() => setProtocol(p)} />
                {p === 'oidc' ? 'OpenID Connect' : 'SAML 2.0'}
              </label>
            ))}
          </fieldset>
          {proto === 'oidc' ? (
            <>
              <p className="text-[12px] text-muted">
                Works with Google Workspace, Microsoft Entra, Okta and other OpenID Connect providers.
              </p>
              <Field label="Redirect (callback) URL to register with your provider">
                {(id) => (
                  <div className="flex gap-2">
                    <input id={id} readOnly value={callback} className={inputCls} />
                    <button type="button" className={btn} onClick={() => copy(callback, 'Callback URL')}>
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
            </>
          ) : (
            <>
              <p className="text-[12px] text-muted">
                Give your identity provider these two values (the metadata URL is usually enough). Assertions
                must be signed; sign-in starts from CNote.
              </p>
              <Field label="Service provider metadata URL">
                {(id) => (
                  <div className="flex gap-2">
                    <input id={id} readOnly value={spMetadata} className={inputCls} />
                    <button
                      type="button"
                      className={btn}
                      aria-label="Copy service provider metadata URL"
                      onClick={() => copy(spMetadata, 'Metadata URL')}
                    >
                      Copy
                    </button>
                  </div>
                )}
              </Field>
              <Field label="Assertion consumer service (ACS) URL">
                {(id) => (
                  <div className="flex gap-2">
                    <input id={id} readOnly value={acs} className={inputCls} />
                    <button
                      type="button"
                      className={btn}
                      aria-label="Copy ACS URL"
                      onClick={() => copy(acs, 'ACS URL')}
                    >
                      Copy
                    </button>
                  </div>
                )}
              </Field>
              <fieldset className="flex flex-wrap gap-4 text-[14px]">
                <legend className="mb-1 text-[12px] font-medium text-muted">
                  Identity provider metadata
                </legend>
                {(['url', 'xml'] as const).map((m) => (
                  <label key={m} className="flex items-center gap-2">
                    <input
                      type="radio"
                      name="samlmode"
                      checked={samlMode === m}
                      onChange={() => setSamlMode(m)}
                    />
                    {m === 'url' ? 'Metadata URL' : 'Paste XML'}
                  </label>
                ))}
              </fieldset>
              {data.sso.saml.has_metadata && (
                <p className="text-[12px] text-muted">
                  Configured for <code className="break-all">{data.sso.saml.idp_entity_id}</code>. Save again
                  to replace it, or to pick up a rotated certificate.
                </p>
              )}
              {samlMode === 'url' ? (
                <Field label="Identity provider metadata URL">
                  {(id) => (
                    <input
                      id={id}
                      name="saml_metadata_url"
                      defaultValue={data.sso.saml.metadata_url}
                      placeholder="https://idp.example.com/metadata"
                      className={inputCls}
                    />
                  )}
                </Field>
              ) : (
                <Field label="Identity provider metadata XML">
                  {(id) => (
                    <textarea id={id} name="saml_metadata" rows={6} className={`${inputCls} font-mono`} />
                  )}
                </Field>
              )}
              <Field label="Email attribute (optional)">
                {(id) => (
                  <input
                    id={id}
                    name="saml_email_attribute"
                    defaultValue={data.sso.saml.email_attribute}
                    placeholder="email"
                    className={inputCls}
                  />
                )}
              </Field>
              <p className="-mt-2 text-[12px] text-muted">
                Defaults to the attributes email, mail or emailaddress, then the NameID if it is an address.
              </p>
            </>
          )}
          <label className="flex items-center gap-2 text-[14px]">
            <input type="checkbox" name="enabled" defaultChecked={data.sso.enabled} /> Enable single sign-on
          </label>
          <label className="flex items-start gap-2 text-[14px]">
            <input type="checkbox" name="enforce" defaultChecked={data.sso.enforce} className="mt-1" />
            <span>
              Require SSO for people on verified domains
              <span className="block text-[12px] text-muted">
                Blocks password sign-in for them. Owners keep password sign-in so a provider outage cannot
                lock you out. New people who sign in this way join as members. Sign-in address:{' '}
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
      )}
    </div>
  )
}
