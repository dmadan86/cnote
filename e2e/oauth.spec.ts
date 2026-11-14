import { createHash, randomBytes } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { newUser, signup } from './helpers'

// The API must run with PUBLIC_BASE_URL set to its own origin (the same as API_URL here).
const API = process.env.API_URL ?? 'http://localhost:4000'
const REDIRECT = 'http://127.0.0.1:7777/callback'

test('consent page: sign in, approve, token works on MCP, disconnect in settings', async ({
  page,
  request,
}) => {
  const reg = await request.post(`${API}/oauth/register`, {
    data: { client_name: 'E2E Agent', redirect_uris: [REDIRECT], token_endpoint_auth_method: 'none' },
  })
  expect(reg.status()).toBe(201)
  const { client_id } = await reg.json()
  const verifier = randomBytes(32).toString('base64url')
  const challenge = createHash('sha256').update(verifier).digest('base64url')
  const qs = new URLSearchParams({
    client_id,
    redirect_uri: REDIRECT,
    response_type: 'code',
    code_challenge: challenge,
    code_challenge_method: 'S256',
    resource: `${API}/mcp`,
    state: 'xyz',
  })
  const consent = `/oauth/authorize?${qs}`

  // Signed out: offered a sign-in that returns here.
  const u = newUser()
  await page.goto(consent)
  await page.getByRole('link', { name: 'Sign in to continue' }).click()
  await expect(page).toHaveURL(/\/login\?next=/)
  // Sign up first (no account yet), then come back to the consent link.
  await signup(page, u)
  await page.goto(consent)

  await expect(page.getByText('E2E Agent')).toBeVisible()
  await expect(page.getByTestId('redirect-host')).toHaveText('127.0.0.1:7777')

  // Nothing listens on the redirect target; capture the browser's return trip instead.
  let returned: URL | undefined
  await page.route(`${REDIRECT}*`, (route) => {
    returned = new URL(route.request().url())
    return route.fulfill({ status: 200, body: 'ok' })
  })
  await page.getByRole('button', { name: 'Approve' }).click()
  await expect.poll(() => returned?.searchParams.get('code')).toBeTruthy()
  expect(returned?.searchParams.get('state')).toBe('xyz')

  const tok = await request.post(`${API}/oauth/token`, {
    form: {
      grant_type: 'authorization_code',
      code: returned?.searchParams.get('code') ?? '',
      redirect_uri: REDIRECT,
      client_id,
      code_verifier: verifier,
    },
  })
  expect(tok.status()).toBe(200)
  const { access_token } = await tok.json()
  const mcp = () =>
    request.post(`${API}/mcp`, {
      headers: { authorization: `Bearer ${access_token}`, accept: 'application/json, text/event-stream' },
      data: { jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} },
    })
  expect((await mcp()).status()).toBe(200)

  // Go-only commands (quote.*) are discoverable and callable with the same token.
  const call = (name: string, args: object) =>
    request.post(`${API}/mcp`, {
      headers: { authorization: `Bearer ${access_token}`, accept: 'application/json, text/event-stream' },
      data: { jsonrpc: '2.0', id: 2, method: 'tools/call', params: { name, arguments: args } },
    })
  const found = await (await call('discover', { query: 'quote send' })).json()
  expect(found.result.structuredContent.commands[0].name).toBe('quote.send')
  const listed = await (await call('invoke', { command: 'quote.list', arguments: {} })).json()
  expect(listed.result.isError).toBe(false)

  await page.goto('/')
  await page.waitForURL(/\/w\/[0-9a-f-]+$/)
  await page.goto(`${page.url()}/settings`)
  await page.getByRole('tab', { name: 'Connected apps' }).click()
  await expect(page.getByText('E2E Agent')).toBeVisible()
  await page.getByRole('button', { name: 'Disconnect E2E Agent' }).click()
  await expect(page.getByText('No connected apps')).toBeVisible()
  expect((await mcp()).status()).toBe(401)
})

test('consent page shows an error and never redirects to an unregistered URI', async ({ page, request }) => {
  const reg = await request.post(`${API}/oauth/register`, {
    data: { client_name: 'Strict', redirect_uris: [REDIRECT] },
  })
  const { client_id } = await reg.json()
  await signup(page)
  const qs = new URLSearchParams({
    client_id,
    redirect_uri: 'https://evil.example/cb',
    response_type: 'code',
    code_challenge: createHash('sha256').update('x').digest('base64url'),
    code_challenge_method: 'S256',
    resource: `${API}/mcp`,
  })
  await page.goto(`/oauth/authorize?${qs}`)
  await expect(page.getByText('redirect_uri does not match')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Approve' })).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'Return to the app' })).toHaveCount(0)
})
