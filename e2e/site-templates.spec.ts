import { expect, type Page, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// The publish test needs the same setup as site-publish.spec.ts (SITES_DOMAIN, SITES_SERVER_TOKEN, SITES_TEST_SKIP_VERIFY).
const port = new URL(process.env.BASE_URL ?? 'http://localhost:3000').port || '3000'

async function start(page: Page, company: Record<string, string>) {
  await signup(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
  const ws = base.split('/')[2]
  const set = await page.request.post(`/api/w/${ws}/commands/company.update`, { data: company })
  expect(set.ok()).toBeTruthy()
  await page.goto(`${base}/site`)
  await expect(page.getByRole('heading', { name: 'Create your website' })).toBeVisible()
  return { base, ws }
}

async function theme(page: Page, ws: string) {
  const sites = await (await page.request.post(`/api/w/${ws}/commands/site.list`, { data: {} })).json()
  const got = await (
    await page.request.post(`/api/w/${ws}/commands/site.get`, { data: { id: sites[0].id } })
  ).json()
  return got.data.root.props.theme as Record<string, string>
}

const card = (page: Page, name: string) =>
  page.getByRole('listitem').filter({ has: page.getByRole('heading', { name, exact: true }) })

test('Templates: filter by type and style, a design keeps its own colour unless the brand colour is ticked', async ({
  page,
}) => {
  const { base, ws } = await start(page, { trade_name: `Gym ${uniq()}`, email_accent: '#7c3aed' })

  // 15 designs; a chip puts the matches first and keeps the rest under "Other designs".
  await expect(page.getByRole('heading', { level: 2 })).toHaveCount(15, { timeout: 30_000 })
  await page.getByRole('group', { name: 'Business type' }).getByRole('button', { name: 'Fitness' }).click()
  await expect(page.getByRole('heading', { name: 'Other designs' })).toBeVisible()
  await expect(page.getByRole('listitem').first().getByRole('heading')).toHaveText('Pulse')
  await page.getByRole('group', { name: 'Style' }).getByRole('button', { name: 'Bold', exact: true }).click()
  await expect(page.getByRole('listitem').first().getByRole('heading')).toHaveText('Pulse')

  // A violet brand colour is unreadable on the dark Pulse page, so the design keeps its lime.
  await page.getByLabel('Use my brand colour').check()
  await expect(card(page, 'Pulse').getByText('too faint')).toBeVisible()
  await card(page, 'Pulse').getByRole('button', { name: 'Use this' }).click()
  await expect(page.getByText('Not published yet')).toBeVisible()
  const th = await theme(page, ws)
  expect(th.primary).toBe('#a3e635')
  expect(th.background).toBe('#0a0a0a')
  expect(base).toContain('/w/')
})

test('Templates: a chosen palette and the brand colour are saved with the site', async ({ page }) => {
  const { ws } = await start(page, { trade_name: `Clinic ${uniq()}`, email_accent: '#7c3aed' })
  await expect(page.getByRole('heading', { level: 2 })).toHaveCount(15, { timeout: 30_000 })

  // Care's layout with Trusty's colours: one swatch per other style, plus the design's own.
  await card(page, 'Care').getByRole('button', { name: 'Preview' }).click()
  const dialog = page.getByRole('dialog')
  const swatches = dialog.getByRole('group', { name: 'Colours' }).getByRole('button')
  await expect(swatches).toHaveCount(5)
  // Hold the themed response so the previous preview cannot be submitted for the new selection.
  let release: (() => void) | undefined
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route('**/commands/site.templates', async (route) => {
    const body = route.request().postDataJSON()
    if (body.key && body.theme) await pending
    await route.continue()
  })
  await dialog.getByRole('button', { name: /^Colours from Trusty$/ }).click()
  await expect(dialog.getByRole('button', { name: 'Use this design' })).toBeDisabled()
  release?.()
  await expect(dialog.getByRole('button', { name: 'Use this design' })).toBeEnabled()
  await expect(dialog.getByRole('button', { name: /^Colours from Trusty$/ })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await dialog.getByRole('button', { name: 'Use this design' }).click()
  await expect(page.getByText('Not published yet')).toBeVisible()
  const th = await theme(page, ws)
  expect(th.primary).toBe('#1d4ed8')
  expect(th.background).toBe('#ffffff')
  expect(th.font_heading).toBe('serif') // the layout's own fonts stay
})

test('Templates: an empty section is not published, and the 360 px header works with a keyboard', async ({
  page,
  browser,
}) => {
  const name = `Plumber ${uniq()}`
  const { base, ws } = await start(page, {
    trade_name: name,
    phone: '+919876543210',
    address: '12 MG Road, Pune',
  })
  await expect(page.getByRole('heading', { level: 2 })).toHaveCount(15, { timeout: 30_000 })
  await card(page, 'Trusty').getByRole('button', { name: 'Use this' }).click()
  await expect(page.getByText('Not published yet')).toBeVisible()

  // The builder shows the empty Services section with a hint; the visitor will not.
  const frame = page.frameLocator('iframe[title="Site preview"]')
  await expect(frame.getByText('Add your services')).toBeVisible()

  await page.getByRole('button', { name: 'Publish', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Publish your website' })
  const sub = `e2e-${uniq()}`
  await dialog.getByLabel('Choose your website address').fill(sub)
  await expect(dialog.getByText(`${sub}.localhost:${port} is free`)).toBeVisible()
  await dialog.getByRole('button', { name: 'Publish website' }).click()
  await expect(page.getByRole('dialog', { name: 'Your website is live' }).getByTestId('live-url')).toHaveText(
    `http://${sub}.localhost:${port}`,
    { timeout: 30_000 },
  )
  expect(ws).toBeTruthy()
  expect(base).toContain('/w/')

  const ctx = await browser.newContext({ viewport: { width: 360, height: 740 } })
  const p = await ctx.newPage()
  await p.goto(`http://${sub}.localhost:${port}/`)
  await expect(p.getByRole('heading', { level: 1 })).toContainText(name)
  await expect(p.getByRole('heading', { name: 'Our services' })).toHaveCount(0)
  await expect(p.getByRole('link', { name: 'Our services' })).toHaveCount(0)
  await expect(p.getByRole('heading', { name: 'What customers say' })).toHaveCount(0)
  await expect(p.getByText('Add your')).toHaveCount(0)

  // No sideways scroll, and the header is reachable with Tab: the one link left, then the button.
  expect(await p.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(360)
  const nav = p.getByRole('navigation', { name: 'Site' })
  await p.keyboard.press('Tab')
  await expect(nav.getByRole('link', { name: 'Find us' })).toBeFocused()
  await p.keyboard.press('Tab')
  const cta = p.locator('header').getByRole('link', { name: 'Contact us' })
  await expect(cta).toBeFocused()
  const box = await cta.boundingBox()
  expect((box?.x ?? -1) + (box?.width ?? 0)).toBeLessThanOrEqual(360)
  await ctx.close()
})
