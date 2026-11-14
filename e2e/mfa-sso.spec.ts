import { createHash, createHmac, createSign, generateKeyPairSync, randomBytes } from 'node:crypto'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, test } from '@playwright/test'
import { newUser, signup } from './helpers'

/** RFC 6238, written out here so the test does not borrow the server's implementation. */
function totp(secretB32: string, step: number): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const c of secretB32.replace(/=+$/, '')) bits += alphabet.indexOf(c).toString(2).padStart(5, '0')
  const key = Buffer.from(bits.match(/.{8}/g)!.map((b) => Number.parseInt(b, 2)))
  const msg = Buffer.alloc(8)
  msg.writeBigUInt64BE(BigInt(step))
  const h = createHmac('sha1', key).update(msg).digest()
  const o = h[h.length - 1] & 15
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1_000_000).padStart(6, '0')
}

test('enrol an authenticator app, then sign in with a code', async ({ page }) => {
  const u = await signup(page, newUser())
  await page.getByRole('button', { name: 'User menu' }).click()
  await page.getByRole('menuitem', { name: 'Account security' }).click()
  await expect(page.getByRole('heading', { name: 'Two-factor authentication is off' })).toBeVisible()
  await page.getByRole('button', { name: 'Set up an authenticator app' }).click()
  await expect(page.getByRole('img', { name: /QR code/ })).toBeVisible()
  const secret = (await page.getByTestId('mfa-secret').innerText()).trim()
  await page.getByLabel(/Enter the 6-digit code/).fill(totp(secret, Math.floor(Date.now() / 30000)))
  await page.getByRole('button', { name: 'Turn on two-factor authentication' }).click()
  await expect(page.getByRole('heading', { name: 'Save your recovery codes' })).toBeVisible()
  await expect(page.getByTestId('recovery-codes').getByRole('listitem')).toHaveCount(10)
  await page.getByRole('button', { name: 'I saved my recovery codes' }).click()
  await expect(page.getByRole('heading', { name: 'Two-factor authentication is on' })).toBeVisible()

  // a fresh browser session: the password alone is not enough any more
  await page.context().clearCookies()
  await page.goto('/login')
  await page.getByLabel('Email').fill(u.email)
  await page.getByLabel('Password').fill(u.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Enter your 6-digit code' })).toBeVisible()
  // the enrolment code is spent; the next 30 s step is inside the accepted window
  await page.getByLabel('Authentication code').fill('000000')
  await page.getByRole('button', { name: 'Verify' }).click()
  await expect(page.getByText('invalid code')).toBeVisible()
  await page.getByLabel('Authentication code').fill(totp(secret, Math.floor(Date.now() / 30000) + 1))
  await page.getByRole('button', { name: 'Verify' }).click()
  await expect(page).toHaveURL(/\/w\/[0-9a-f-]+$/)
})

/** A minimal OpenID Connect provider: discovery, auto-approving authorize, token, JWKS. */
function fakeProvider(clientId: string, secret: string, email: string) {
  const { publicKey, privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
  const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'k1', use: 'sig', alg: 'RS256' }
  const grants = new Map<string, { nonce: string; challenge: string; redirect: string }>()
  const b64u = (b: Buffer | string) => Buffer.from(b).toString('base64url')
  let base = ''
  const server: Server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', base)
    const json = (o: unknown) => {
      res.setHeader('content-type', 'application/json')
      res.end(JSON.stringify(o))
    }
    if (url.pathname === '/.well-known/openid-configuration')
      return json({
        issuer: base,
        authorization_endpoint: `${base}/authorize`,
        token_endpoint: `${base}/token`,
        jwks_uri: `${base}/jwks`,
      })
    if (url.pathname === '/jwks') return json({ keys: [jwk] })
    if (url.pathname === '/authorize') {
      const code = randomBytes(8).toString('hex')
      const q = url.searchParams
      grants.set(code, {
        nonce: q.get('nonce') ?? '',
        challenge: q.get('code_challenge') ?? '',
        redirect: q.get('redirect_uri') ?? '',
      })
      res.statusCode = 302
      res.setHeader(
        'location',
        `${q.get('redirect_uri')}?code=${code}&state=${encodeURIComponent(q.get('state') ?? '')}`,
      )
      return res.end()
    }
    if (url.pathname === '/token') {
      let body = ''
      req.on('data', (c) => {
        body += c
      })
      req.on('end', () => {
        const f = new URLSearchParams(body)
        const g = grants.get(f.get('code') ?? '')
        const ok =
          g &&
          f.get('client_secret') === secret &&
          f.get('client_id') === clientId &&
          createHash('sha256')
            .update(f.get('code_verifier') ?? '')
            .digest('base64url') === g.challenge
        if (!ok) {
          res.statusCode = 400
          return res.end('{}')
        }
        const now = Math.floor(Date.now() / 1000)
        const head = b64u(JSON.stringify({ alg: 'RS256', kid: 'k1' }))
        const claims = b64u(
          JSON.stringify({
            iss: base,
            aud: clientId,
            sub: 'e2e',
            iat: now,
            exp: now + 600,
            nonce: g.nonce,
            email,
            email_verified: true,
            name: 'E2E Person',
          }),
        )
        const sig = createSign('RSA-SHA256')
          .update(`${head}.${claims}`)
          .sign(privateKey)
          .toString('base64url')
        json({ id_token: `${head}.${claims}.${sig}`, token_type: 'Bearer' })
      })
      return
    }
    res.statusCode = 404
    res.end()
  })
  return new Promise<{ base: string; close: () => void }>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
      resolve({ base, close: () => server.close() })
    })
  })
}

test('SSO sign-in against a provider configured in workspace settings', async ({ page, browser }) => {
  const id = randomBytes(4).toString('hex')
  const clientId = `client-${id}`
  const secret = randomBytes(12).toString('hex')
  const email = `sso-${id}@e2e-sso.test`
  const idp = await fakeProvider(clientId, secret, email)
  try {
    await signup(page, newUser())
    const me = await (await page.request.get('/api/auth/me')).json()
    const slug: string = me.workspaces[0].slug
    await page.getByRole('link', { name: 'Settings' }).first().click()
    await page.getByRole('tab', { name: 'Security' }).click()
    await page.getByLabel('Issuer URL').fill(idp.base)
    await page.getByLabel('Client ID').fill(clientId)
    await page.getByLabel('Client secret').fill(secret)
    await page.getByLabel(/Allowed email domains/).fill('e2e-sso.test')
    await page.getByLabel('Enable single sign-on').check()
    await page.getByRole('button', { name: 'Save single sign-on' }).click()
    await expect(page.getByText('Single sign-on settings saved.')).toBeVisible()
    await expect(page.getByText('Secret configured')).toBeVisible()

    // someone else, in a clean browser, signs in through the provider and lands in the workspace as a member
    const ctx = await browser.newContext()
    const p2 = await ctx.newPage()
    await p2.goto('/login')
    await p2.getByRole('button', { name: 'Use company single sign-on' }).click()
    await p2.getByLabel('Workspace address').fill(slug)
    await p2.getByRole('button', { name: 'Continue' }).click()
    await expect(p2).toHaveURL(/\/w\/[0-9a-f-]+/)
    const me2 = await (await p2.request.get('/api/auth/me')).json()
    expect(me2.user.email).toBe(email)
    expect(me2.workspaces).toHaveLength(1)
    expect(me2.workspaces[0].role).toBe('member')

    // an unknown workspace comes back to the sign-in page with the reason
    const p3 = await (await browser.newContext()).newPage()
    await p3.goto(`/api/auth/sso/start?workspace=nope-${id}`)
    await expect(p3).toHaveURL(/\/login\?sso_error=/)
    await expect(p3.getByText(/not found/)).toBeVisible()
    await ctx.close()
  } finally {
    idp.close()
  }
})
