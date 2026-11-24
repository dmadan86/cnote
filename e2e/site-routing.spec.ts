import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Custom-domain routing, without DNS or a connected domain. Chromium maps *.cnote-e2e.test to 127.0.0.1
// (playwright.config.ts), so http://www.x.cnote-e2e.test:<port> reaches the web server with that Host header.
// The web server needs SITES_CUSTOM_DOMAINS=1 (CI sets it). Connecting a real domain (Pro, TXT proof) is
// covered by Go tests and by site-domain.spec.ts once the API's SITES_TEST_DNS helper lands.
const port = new URL(process.env.BASE_URL ?? 'http://localhost:3000').port || '3000'

test('Custom-domain host: only site paths answer, with site headers and no cookies', async ({
  page,
  context,
}) => {
  const host = `http://www.e2e-${Date.now()}.cnote-e2e.test:${port}`
  for (const path of ['/login', '/api/auth/me', '/w', '/_next/foo', '/published/x']) {
    const r = await page.goto(`${host}${path}`)
    expect(r?.status(), path).toBe(404)
  }
  // An unknown host is a site host: the page answers 404 (no such site), never the app.
  const home = await page.goto(`${host}/`)
  expect(home?.status()).toBe(404)
  const csp = home?.headers()['content-security-policy'] ?? ''
  expect(csp).toContain("frame-ancestors 'none'")
  expect(csp).toMatch(/frame-src [^;]*youtube-nocookie\.com/)
  expect(home?.headers()['set-cookie']).toBeUndefined()
  expect(await context.cookies(host)).toEqual([])
  // The app itself is untouched.
  expect((await page.goto('/login'))?.status()).toBe(200)
})

// Click-to-load embeds (design 17.7). Puck's drag does not work with synthetic input, so the Embed block is added
// through the same site.edit command the editor uses.
test('Embed: no request to the provider before the click', async ({ page, browser }) => {
  await signup(page)
  const ws = new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
  await page.goto(`/w/${ws}/site`)
  await page.getByRole('button', { name: 'Use this' }).first().click()
  await expect(page.getByText('Not published yet')).toBeVisible()
  const cmd = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
    return r.json()
  }
  const [site] = await cmd('site.list', {})
  const got = await cmd('site.get', { id: site.id })
  const edited = await cmd('site.edit', {
    id: site.id,
    expected_version_id: got.site.draft_version_id,
    ops: [
      {
        tool: 'add_block',
        input: { type: 'Embed', props: { provider: 'youtube', source: 'dQw4w9WgXcQ', title: 'Our clinic' } },
      },
    ],
  })
  const sub = `e2e-${uniq()}`
  await cmd('site.publish', {
    id: site.id,
    expected_version_id: edited.site.draft_version_id,
    subdomain: sub,
  })

  const ctx = await browser.newContext()
  const seen: string[] = []
  await ctx.route(/youtube/, (r) => {
    seen.push(r.request().url())
    return r.abort()
  })
  const v = await ctx.newPage()
  await v.goto(`http://${sub}.localhost:${port}/`)
  const load = v.getByRole('link', { name: /Load video/ })
  await expect(load).toBeVisible()
  expect(seen).toEqual([])
  await load.click()
  await expect(v.locator('iframe[src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"]')).toBeAttached()
  await expect.poll(() => seen.length).toBeGreaterThan(0)
  await ctx.close()
})
