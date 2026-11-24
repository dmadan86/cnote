import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

// Published catalogue: the item sheet opened by ?item=, WhatsApp and question buttons, and a hidden price that
// never reaches the HTML. Needs the same env as site-publish.spec.ts.
const port = new URL(process.env.BASE_URL ?? 'http://localhost:3000').port || '3000'

test('Catalogue: sheet by ?item=, enquiry buttons, and no price in the HTML when hidden', async ({
  page,
  browser,
}) => {
  await signup(page)
  await createSpace(page)
  const ws = new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
  const cmd = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
    return r.json()
  }
  await cmd('company.update', { trade_name: `Sharma Sweets ${uniq()}`, phone: '+919876543210' })
  const sku = uniq()
  await cmd('stock_item.create', {
    sku: `K-${sku}`,
    name: 'Kaju katli',
    unit: 'kg',
    price_minor: 100_000,
    gst_rate_bp: 1800,
  })
  const items = await cmd('stock_item.list', {})
  const id = items.find((i: { name: string }) => i.name === 'Kaju katli').id as string

  await page.goto(`/w/${ws}/site`)
  await page.getByRole('button', { name: 'Use this' }).first().click()
  await expect(page.getByText('Not published yet')).toBeVisible()
  const [site] = await cmd('site.list', {})
  const got = await cmd('site.get', { id: site.id })
  const edited = await cmd('site.edit', {
    id: site.id,
    expected_version_id: got.site.draft_version_id,
    ops: [{ tool: 'add_block', input: { type: 'Catalogue', props: { title: 'Our products' } } }],
  })
  await cmd('site.catalog_set', { id: site.id, price_mode: 'show_incl', items: [{ item_id: id }] })
  const sub = `e2e-${uniq()}`
  await cmd('site.publish', {
    id: site.id,
    expected_version_id: edited.site.draft_version_id,
    subdomain: sub,
  })

  const siteUrl = `http://${sub}.localhost:${port}`
  const ctx = await browser.newContext()
  const v = await ctx.newPage()
  expect((await v.goto(`${siteUrl}/`))?.status()).toBe(200)
  await expect(v.getByRole('heading', { name: 'Our products' })).toBeVisible()
  await expect(v.getByRole('button', { name: /Kaju katli/ })).toContainText('₹1,180')
  await v.getByRole('button', { name: /Kaju katli/ }).click()
  const sheet = v.getByRole('dialog', { name: 'Kaju katli' })
  await expect(sheet).toBeVisible()
  expect(new URL(v.url()).searchParams.get('item')).toBe(id)
  await expect(sheet.getByRole('link', { name: 'Enquire on WhatsApp' })).toHaveAttribute(
    'href',
    "https://wa.me/919876543210?text=Hi%2C%20I'm%20interested%20in%20Kaju%20katli",
  )
  // A shared link opens the same sheet; the server never reads the query.
  await v.goto(`${siteUrl}/?item=${id}`)
  await expect(v.getByRole('dialog', { name: 'Kaju katli' })).toBeVisible()
  await v.keyboard.press('Escape')
  await expect(v.getByRole('dialog')).toHaveCount(0)
  // Closing the sheet goes back in history; let that finish before the next load.
  await expect(v).not.toHaveURL(/[?&]item=/)
  await v.goto(`${siteUrl}/?item=00000000-0000-4000-8000-000000000000`)
  await expect(v.getByRole('dialog')).toHaveCount(0)

  // Hide the price: re-snapshot, then no amount is anywhere in the HTML, JSON or message.
  await cmd('site.catalog_set', { id: site.id, price_mode: 'hide', items: [{ item_id: id }] })
  await cmd('site.catalog_sync', { id: site.id })
  const fresh = await browser.newContext()
  const html = await (await fresh.request.get(`${siteUrl}/`)).text()
  expect(html).toContain('Kaju katli')
  expect(html).toContain('Ask for price')
  expect(html).not.toMatch(/₹\s?[\d,]+|\b1,?180\b|118000|100000/) // any rupee amount or the item's price in paise
  await fresh.close()
  await ctx.close()
})
