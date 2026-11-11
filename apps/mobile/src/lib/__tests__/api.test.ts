import { ApiError, createApi, normalizeUrl } from '../api'

const reply = (status: number, body: unknown) =>
  jest.fn(async () => new Response(typeof body === 'string' ? body : JSON.stringify(body), { status }))

describe('api client', () => {
  it('sends the session token as a bearer header and posts commands', async () => {
    const f = reply(200, [{ id: 'n1' }])
    const api = createApi({ baseUrl: 'http://h:4000/', token: 'cs_abc', fetchImpl: f as any })
    expect(await api.cmd('w1', 'note.list', { limit: 5 })).toEqual([{ id: 'n1' }])
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://h:4000/api/w/w1/commands/note.list')
    expect(init.method).toBe('POST')
    expect((init.headers as any).authorization).toBe('Bearer cs_abc')
    expect(init.body).toBe('{"limit":5}')
  })

  it('omits authorization without a token and marks login as native', async () => {
    const f = reply(200, { token: 'cs_x', user_id: 'u' })
    const api = createApi({ baseUrl: 'http://h', fetchImpl: f as any })
    await api.login({ email: 'a@b.c', password: 'pw' })
    const headers = (f.mock.calls[0] as unknown as [string, RequestInit])[1].headers as any
    expect(headers.authorization).toBeUndefined()
    expect(headers['x-cnote-client']).toBe('native')
  })

  it('maps the server error envelope', async () => {
    const api = createApi({
      baseUrl: 'http://h',
      token: 't',
      fetchImpl: reply(403, { error: { code: 'forbidden', message: 'no access' } }) as any,
    })
    await expect(api.cmd('w', 'x')).rejects.toMatchObject({
      code: 'forbidden',
      message: 'no access',
      status: 403,
    })
  })

  it('falls back to unauthorized for a bare 401 and internal otherwise', async () => {
    const a = createApi({ baseUrl: 'http://h', fetchImpl: reply(401, '') as any })
    await expect(a.me()).rejects.toMatchObject({ code: 'unauthorized', status: 401 })
    const b = createApi({ baseUrl: 'http://h', fetchImpl: reply(500, '<html>') as any })
    await expect(b.me()).rejects.toMatchObject({ code: 'internal', status: 500 })
  })

  it('maps network failures and unreadable success bodies', async () => {
    const down = createApi({
      baseUrl: 'http://h',
      fetchImpl: jest.fn(async () => {
        throw new TypeError('Network request failed')
      }) as any,
    })
    const err = await down.me().catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.code).toBe('network')
    const junk = createApi({ baseUrl: 'http://h', fetchImpl: reply(200, 'not json') as any })
    await expect(junk.me()).rejects.toMatchObject({ code: 'bad_response' })
  })

  it('normalizes server URLs', () => {
    expect(normalizeUrl(' http://h:4000// ')).toBe('http://h:4000')
  })
})
