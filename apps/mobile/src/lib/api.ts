/** Error codes mirror the server's `{ error: { code, message } }` envelope, plus client-side `network` / `bad_response`. */
export class ApiError extends Error {
  constructor(
    public code: string,
    message: string,
    public status: number,
  ) {
    super(message)
  }
}

export type Me = {
  user: { id: string; email: string; name: string }
  workspaces: { id: string; name: string; slug: string; role: string }[]
}

type Opts = { baseUrl: string; token?: string | null; fetchImpl?: typeof fetch }

/** Strip whitespace and trailing slashes so `http://host:4000/ ` and `http://host:4000` are the same server. */
export const normalizeUrl = (u: string) => u.trim().replace(/\/+$/, '')

export function createApi({ baseUrl, token, fetchImpl }: Opts) {
  const f = fetchImpl ?? fetch
  const base = normalizeUrl(baseUrl)

  async function request<T>(path: string, init: { body?: unknown; native?: boolean; method?: string } = {}) {
    const headers: Record<string, string> = {}
    if (init.body !== undefined) headers['content-type'] = 'application/json'
    if (token) headers.authorization = `Bearer ${token}`
    // Asks login/signup to return the session token in the body (browsers get a cookie instead).
    if (init.native) headers['x-cnote-client'] = 'native'
    let res: Response
    try {
      res = await f(base + path, {
        method: init.method ?? (init.body !== undefined ? 'POST' : 'GET'),
        headers,
        body: init.body !== undefined ? JSON.stringify(init.body) : undefined,
      })
    } catch {
      throw new ApiError('network', `Cannot reach ${base}. Check the server URL and your connection.`, 0)
    }
    const text = await res.text()
    let data: any = {}
    if (text) {
      try {
        data = JSON.parse(text)
      } catch {
        if (res.ok) throw new ApiError('bad_response', 'The server sent an unreadable response.', res.status)
      }
    }
    if (!res.ok) {
      const e = data?.error ?? {}
      throw new ApiError(
        e.code ?? (res.status === 401 ? 'unauthorized' : 'internal'),
        e.message ?? (res.statusText || `Request failed (${res.status})`),
        res.status,
      )
    }
    return data as T
  }

  return {
    request,
    cmd: <T = unknown>(ws: string, name: string, input: unknown = {}) =>
      request<T>(`/api/w/${ws}/commands/${name}`, { body: input }),
    me: () => request<Me>('/api/auth/me'),
    login: (b: { email: string; password: string }) =>
      request<{ token: string; user_id: string }>('/api/auth/login', { body: b, native: true }),
    signup: (b: { name: string; email: string; password: string; workspace_name: string }) =>
      request<{ token: string; user_id: string }>('/api/auth/signup', { body: b, native: true }),
    logout: () => request('/api/auth/logout', { method: 'POST' }),
  }
}

export type Api = ReturnType<typeof createApi>
