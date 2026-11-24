import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Needs SITES_DOMAIN=localhost:3000 on the API and the web server, SITES_SERVER_TOKEN on both, and
// SITES_TEST_SKIP_VERIFY=1 on the API (CI sets all three). Chromium resolves *.localhost itself, so the test
// navigates the browser to http://<sub>.localhost:<port>. Each visit uses a fresh context: pages are served with
// Cache-Control: public, max-age=60 and a warm browser cache would hide an unpublish.

const port = new URL(process.env.BASE_URL ?? 'http://localhost:3000').port || '3000'

test('Website: publish, visit, send an enquiry, take offline', async ({ page, browser, baseURL }) => {
  await signup(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
  const ws = base.split('/')[2]
  const name = `Pune Dental ${uniq()}`
  const set = await page.request.post(`/api/w/${ws}/commands/company.update`, {
    data: { trade_name: name, phone: '+919876543210' },
  })
  expect(set.ok()).toBeTruthy()

  await page.goto(`${base}/site`)
  await page.getByRole('button', { name: 'Use this' }).first().click()
  await expect(page.getByText('Not published yet')).toBeVisible()
  await expect(page.locator('header').getByText('Draft', { exact: true })).toBeVisible()

  // First publish: the address field with its fixed suffix, checked as we type.
  await page.getByRole('button', { name: 'Publish', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Publish your website' })
  const sub = `e2e-${uniq()}`
  await dialog.getByLabel('Choose your website address').fill('www')
  await expect(dialog.getByText("can't be used")).toBeVisible()
  await dialog.getByLabel('Choose your website address').fill(sub)
  await expect(dialog.getByText(`${sub}.localhost:${port} is free`)).toBeVisible()
  await expect(dialog.getByText('New enquiries appear in Leads')).toBeVisible()
  await dialog.getByRole('button', { name: 'Publish website' }).click()
  const live = page.getByRole('dialog', { name: 'Your website is live' })
  await expect(live.getByTestId('live-url')).toHaveText(`http://${sub}.localhost:${port}`, {
    timeout: 30_000,
  })
  await expect(live.getByRole('button', { name: 'Copy link' })).toBeVisible()
  await expect(live.getByRole('link', { name: 'Share on WhatsApp' })).toBeVisible()
  await expect(live.getByRole('link', { name: /Visit website/ })).toBeVisible()
  await live.getByRole('button', { name: 'Close' }).click()
  await expect(page.locator('header span.rounded-full', { hasText: /^Published$/ })).toBeVisible()

  // The live site: server-rendered, no frame, the badge, a CSP, and no cookie.
  const siteUrl = `http://${sub}.localhost:${port}`
  const visit = async (url: string) => {
    const ctx = await browser.newContext()
    const p = await ctx.newPage()
    const res = await p.goto(url)
    return { ctx, p, res }
  }
  const v = await visit(`${siteUrl}/`)
  expect(v.res?.status()).toBe(200)
  await expect(v.p.getByRole('heading', { level: 1 })).toContainText(name)
  await expect(v.p.locator('iframe')).toHaveCount(0)
  await expect(v.p.getByText('Made with C Note')).toBeVisible()
  await expect(v.p.getByRole('link', { name: 'Report this site' })).toBeVisible()
  expect(v.res?.headers()['content-security-policy']).toContain("frame-ancestors 'none'")
  expect(v.res?.headers()['cache-control']).toContain('max-age=60')
  expect(await v.ctx.cookies(siteUrl)).toEqual([])

  // The app is unreachable on a site host, and tenant pages do not render on the app host.
  for (const path of ['/login', '/api/auth/me', '/w']) {
    const r = await v.p.goto(`${siteUrl}${path}`)
    expect(r?.status(), path).toBe(404)
  }
  expect((await v.p.goto(`${siteUrl}/robots.txt`))?.status()).toBe(200)
  expect((await v.p.goto(`${siteUrl}/sitemap.xml`))?.status()).toBe(200)
  expect((await v.p.goto(`${siteUrl.replace(sub, `nope-${uniq()}`)}/`))?.status()).toBe(404)
  await v.ctx.close()
  expect((await page.request.get(`/published/${sub}`)).status()).toBe(404)

  // A visitor sends an enquiry: consent unticked and required, then thanks.
  const w = await visit(`${siteUrl}/`)
  const form = w.p.locator('form').filter({ has: w.p.getByLabel(/I agree that/) })
  await form.getByLabel('Your name').fill('Priya Visitor')
  await form.getByLabel('Mobile number').fill('9876501234')
  await form.getByLabel('Message').fill('Do you do braces?')
  await form.getByRole('button').last().click()
  await expect(form.getByText('Tick the box')).toBeVisible()
  await form.getByLabel(/I agree that/).check()
  await form.getByRole('button').last().click()
  await expect(w.p.getByRole('status')).toContainText("We've got your message", { timeout: 30_000 })
  await w.ctx.close()

  // The owner sees the enquiry chip and the lead.
  await page.reload()
  const chip = page.getByRole('link', { name: /1 new enquiry/ })
  await expect(chip).toBeVisible({ timeout: 30_000 })
  await chip.click()
  await expect(page.getByText('Priya Visitor').first()).toBeVisible()

  // Take it offline from settings; the site is gone.
  await page.goto(`${base}/site`)
  await page.getByRole('button', { name: 'Website settings' }).click()
  const panel = page.getByRole('complementary', { name: 'Website settings' })
  await panel.getByRole('button', { name: 'Take offline' }).click()
  await page
    .getByRole('dialog', { name: 'Take your website offline?' })
    .getByRole('button', { name: 'Take offline' })
    .click()
  await expect(page.locator('header').getByText('Offline', { exact: true })).toBeVisible()
  const gone = await visit(`${siteUrl}/`)
  expect(gone.res?.status()).toBe(404)
  await gone.ctx.close()
})
