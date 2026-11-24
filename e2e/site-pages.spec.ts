import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Same environment as site-publish.spec.ts. Adds a page, publishes, and visits it by host.
const port = new URL(process.env.BASE_URL ?? 'http://localhost:3000').port || '3000'

test('Website: add an About page, publish, open /about, find it in the sitemap', async ({
  page,
  browser,
}) => {
  await signup(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
  await page.goto(`${base}/site`)
  await page.getByRole('button', { name: 'Use this' }).first().click()
  await expect(page.getByText('Not published yet')).toBeVisible()

  await page.getByRole('button', { name: /^Page:/ }).click()
  await page.getByRole('menuitem', { name: 'Add page' }).click()
  const add = page.getByRole('dialog', { name: 'Add a page' })
  await expect(add.getByText('Template — not legal advice').first()).toBeVisible()
  await add.getByRole('button', { name: 'Add page' }).click()
  await expect(page.getByRole('button', { name: 'Page: About us' })).toBeVisible()

  // A privacy page carries the builder-only banner.
  await page.getByRole('button', { name: /^Page:/ }).click()
  await page.getByRole('menuitem', { name: 'Add page' }).click()
  await page.getByRole('dialog', { name: 'Add a page' }).getByLabel('Privacy policy').check()
  await page.getByRole('dialog', { name: 'Add a page' }).getByRole('button', { name: 'Add page' }).click()
  await expect(page.getByText('Template — not legal advice. Read it')).toBeVisible()

  await page.getByRole('button', { name: 'Publish', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Publish your website' })
  const sub = `e2e-${uniq()}`
  await dialog.getByLabel('Choose your website address').fill(sub)
  await expect(dialog.getByText(`${sub}.localhost:${port} is free`)).toBeVisible()
  await dialog.getByRole('button', { name: 'Publish website' }).click()
  const siteUrl = `http://${sub}.localhost:${port}`
  await expect(page.getByRole('dialog', { name: 'Your website is live' }).getByTestId('live-url')).toHaveText(
    siteUrl,
    { timeout: 30_000 },
  )

  const ctx = await browser.newContext()
  const v = await ctx.newPage()
  expect((await v.goto(`${siteUrl}/about-us`))?.status()).toBe(200)
  expect((await v.goto(`${siteUrl}/privacy`))?.status()).toBe(200)
  expect((await v.goto(`${siteUrl}/nope`))?.status()).toBe(404)
  expect((await v.goto(`${siteUrl}/api`))?.status()).toBe(404) // reserved
  const map = await v.goto(`${siteUrl}/sitemap.xml`)
  const xml = (await map?.text()) ?? ''
  expect(xml).toContain(`${siteUrl}/about-us`)
  expect(xml).toContain(`${siteUrl}/privacy`)
  expect(await ctx.cookies(siteUrl)).toEqual([])
  await ctx.close()
})
