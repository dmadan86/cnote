'use client'
import { useSearchParams } from 'next/navigation'
import { Suspense, useEffect, useState } from 'react'
import { btn, btnPrimary, ErrorText, Field, inputCls } from '@/components/ui'
import { api } from '@/lib/api'

type Ctx =
  | {
      ok: true
      client_name: string
      redirect_host: string
      workspaces: { id: string; name: string; role: string }[]
    }
  | { ok: false; error: string; description: string; redirect_to: string | null }

/** OAuth authorization endpoint (MCP clients land here). All validation is server-side; this page only shows it. */
function Consent() {
  const search = useSearchParams()
  const params = Object.fromEntries(search.entries())
  const [ctx, setCtx] = useState<Ctx>()
  const [signedOut, setSignedOut] = useState(false)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)

  // biome-ignore lint/correctness/useExhaustiveDependencies: params derive from the URL string
  useEffect(() => {
    api<Ctx>('/api/oauth/authorize/context', { body: params })
      .then(setCtx)
      .catch((e: Error & { status?: number }) =>
        e.status === 401 ? setSignedOut(true) : setError(e.message),
      )
  }, [search.toString()])

  async function decide(approve: boolean, workspaceId: string) {
    setBusy(true)
    setError(undefined)
    try {
      const r = await api<{ ok: boolean; redirect_to?: string; description?: string }>(
        '/api/oauth/authorize',
        {
          body: { ...params, workspace_id: workspaceId, approve },
        },
      )
      if (r.redirect_to) window.location.assign(r.redirect_to)
      else setError(r.description ?? 'Could not complete the request')
    } catch (e) {
      setError((e as Error).message)
    }
    setBusy(false)
  }

  const here = `/oauth/authorize?${search.toString()}`
  return (
    <main className="flex min-h-screen items-center justify-center bg-side p-4">
      <div className="w-full max-w-sm space-y-4 rounded-lg border border-line bg-canvas p-6">
        <h1 className="text-lg font-semibold">Connect an app to C Note</h1>
        {signedOut && (
          <>
            <p className="text-[14px] text-muted">Sign in to review this request.</p>
            <a className={`${btnPrimary} w-full`} href={`/login?next=${encodeURIComponent(here)}`}>
              Sign in to continue
            </a>
          </>
        )}
        {ctx && !ctx.ok && (
          <>
            <p role="alert" className="text-[14px] text-bad">
              {ctx.description}
            </p>
            {ctx.redirect_to && (
              <a className={`${btn} w-full`} href={ctx.redirect_to}>
                Return to the app
              </a>
            )}
          </>
        )}
        {ctx?.ok && <ConsentForm ctx={ctx} busy={busy} decide={decide} />}
        <ErrorText error={error} />
        {!ctx && !signedOut && !error && <p className="text-[14px] text-muted">Loading...</p>}
      </div>
    </main>
  )
}

function ConsentForm({
  ctx,
  busy,
  decide,
}: {
  ctx: Extract<Ctx, { ok: true }>
  busy: boolean
  decide: (approve: boolean, workspaceId: string) => void
}) {
  const [ws, setWs] = useState(ctx.workspaces[0]?.id ?? '')
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        decide(true, ws)
      }}
    >
      <p className="text-[14px]">
        <strong>{ctx.client_name}</strong> wants to use C Note on your behalf through MCP.
      </p>
      <p className="text-[13px] text-muted">
        After you decide, your browser returns to{' '}
        <strong data-testid="redirect-host">{ctx.redirect_host}</strong>. Only continue if you started this
        connection and recognise that address.
      </p>
      <Field label="Workspace">
        {(id) => (
          <select id={id} value={ws} onChange={(e) => setWs(e.target.value)} className={inputCls}>
            {ctx.workspaces.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name} ({w.role})
              </option>
            ))}
          </select>
        )}
      </Field>
      <p className="text-[12px] text-muted">
        The app can do what you can in this workspace, up to admin, and never as owner. Money-moving and
        outbound actions still need your approval. You can disconnect it any time in Settings, Connected apps.
      </p>
      <div className="flex gap-2">
        <button type="button" className={`${btn} flex-1`} disabled={busy} onClick={() => decide(false, ws)}>
          Deny
        </button>
        <button type="submit" className={`${btnPrimary} flex-1`} disabled={busy || !ws}>
          Approve
        </button>
      </div>
    </form>
  )
}

export default function Page() {
  return (
    <Suspense>
      <Consent />
    </Suspense>
  )
}
