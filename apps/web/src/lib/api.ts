import type { PageStatus } from './docs'

export type ApiError = { code: string; message: string }

export class CmdFailure extends Error {
  constructor(
    public code: string,
    message: string,
    public status: number,
  ) {
    super(message)
  }
}

/** Browser calls go through the Next rewrite (/api/*) so the session cookie stays first-party. */
export async function api<T>(
  path: string,
  init?: { method?: string; body?: unknown; signal?: AbortSignal },
): Promise<T> {
  const hasBody = init?.body !== undefined
  const res = await fetch(path, {
    method: init?.method ?? (hasBody ? 'POST' : 'GET'),
    headers: hasBody ? { 'content-type': 'application/json' } : undefined,
    body: hasBody ? JSON.stringify(init?.body) : undefined,
    credentials: 'same-origin',
    signal: init?.signal,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const e = (data?.error ?? {}) as Partial<ApiError>
    throw new CmdFailure(e.code ?? 'internal', e.message ?? res.statusText, res.status)
  }
  return data as T
}

export const cmd = <T = unknown>(ws: string, name: string, input: unknown = {}, signal?: AbortSignal) =>
  api<T>(`/api/w/${ws}/commands/${name}`, { body: input, signal })

export type Me = {
  user: { id: string; email: string; name: string }
  workspaces: { id: string; name: string; slug: string; role: string }[]
}

export type SessionRow = {
  id: string
  user_agent: string
  ip: string
  amr: 'pwd' | 'mfa' | 'sso'
  created_at: string
  last_seen_at: string | null
  current: boolean
}

export const auth = {
  me: () => api<Me>('/api/auth/me'),
  signup: (b: { name: string; email: string; password: string; workspace_name: string }) =>
    api<{ user_id: string; workspace_id: string | null }>('/api/auth/signup', { body: b }),
  login: (b: { email: string; password: string }) =>
    api<{ user_id?: string; mfa_required?: boolean; mfa_token?: string; mfa_setup_required?: boolean }>(
      '/api/auth/login',
      { body: b },
    ),
  mfaVerify: (b: { mfa_token: string; code: string }) =>
    api<{ user_id: string; mfa_setup_required?: boolean }>('/api/auth/mfa/verify', { body: b }),
  mfaStatus: () => api<{ enabled: boolean; recovery_codes_left: number }>('/api/auth/mfa'),
  mfaSetup: () =>
    api<{ secret: string; otpauth_uri: string }>('/api/auth/mfa/setup', { method: 'POST', body: {} }),
  mfaConfirm: (code: string) =>
    api<{ recovery_codes: string[] }>('/api/auth/mfa/confirm', { body: { code } }),
  mfaDisable: (b: { password: string; code: string }) => api('/api/auth/mfa/disable', { body: b }),
  mfaRecovery: (b: { password: string; code: string }) =>
    api<{ recovery_codes: string[] }>('/api/auth/mfa/recovery', { body: b }),
  sessions: () => api<{ sessions: SessionRow[] }>('/api/auth/sessions'),
  revokeSession: (id: string) => api('/api/auth/sessions/revoke', { body: { id } }),
  revokeOtherSessions: () =>
    api<{ revoked: number }>('/api/auth/sessions/revoke_others', { method: 'POST', body: {} }),
  logout: () => api('/api/auth/logout', { method: 'POST' }),
  createWorkspace: (name: string) => api<{ id: string }>('/api/workspaces', { body: { name } }),
}

export type Space = { id: string; key: string; name: string; template: string; visibility: string }

export type Note = {
  id: string
  space_id: string
  parent_id: string | null
  title: string
  types: string[]
  content_md: string
  owner_id: string | null
  verify_by: string | null
  verified_at: string | null
  freshness: string
  updated_at: string
  version: number
  restriction: 'none' | 'edit' | 'view'
  /** Live-editing generation; a restore bumps it and open editors reload. */
  collab_epoch: number
  /** Docs parity A1 (lib/docs.ts). Absent until the API returns them. */
  status?: PageStatus | null
  icon?: string | null
  wide?: boolean
}

export type SharedNote = Note & { level: 'view' | 'edit' }

export type NoteDetail = Note & {
  /** What the caller may do with this page. */
  level: 'view' | 'edit'
  relations: { id: string; from_id: string; to_id: string; kind: string; other_title: string | null }[]
}

export type Member = { user_id: string; email: string; name: string; role: string }
