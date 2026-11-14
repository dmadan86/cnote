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
export async function api<T>(path: string, init?: { method?: string; body?: unknown }): Promise<T> {
  const hasBody = init?.body !== undefined
  const res = await fetch(path, {
    method: init?.method ?? (hasBody ? 'POST' : 'GET'),
    headers: hasBody ? { 'content-type': 'application/json' } : undefined,
    body: hasBody ? JSON.stringify(init?.body) : undefined,
    credentials: 'same-origin',
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const e = (data?.error ?? {}) as Partial<ApiError>
    throw new CmdFailure(e.code ?? 'internal', e.message ?? res.statusText, res.status)
  }
  return data as T
}

export const cmd = <T = unknown>(ws: string, name: string, input: unknown = {}) =>
  api<T>(`/api/w/${ws}/commands/${name}`, { body: input })

export type Me = {
  user: { id: string; email: string; name: string }
  workspaces: { id: string; name: string; slug: string; role: string }[]
}

export const auth = {
  me: () => api<Me>('/api/auth/me'),
  signup: (b: { name: string; email: string; password: string; workspace_name: string }) =>
    api<{ user_id: string; workspace_id: string | null }>('/api/auth/signup', { body: b }),
  login: (b: { email: string; password: string }) => api<{ user_id: string }>('/api/auth/login', { body: b }),
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
}

export type SharedNote = Note & { level: 'view' | 'edit' }

export type NoteDetail = Note & {
  /** What the caller may do with this page. */
  level: 'view' | 'edit'
  relations: { id: string; from_id: string; to_id: string; kind: string; other_title: string | null }[]
}

export type Member = { user_id: string; email: string; name: string; role: string }
