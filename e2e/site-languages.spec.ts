import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Languages (design 18). Needs the same env as site-publish.spec.ts plus SITES_LANGUAGES=1 on the API and the web
// server. Hindi text is typed through the review sheet, so no model call is needed.
const port = new URL(process.env.BASE_URL ?? 'http://localhost:3000').port || '3000'

test('Languages: add Hindi, edit one translation, publish, /hi/ shows it; an edited source shows English again', async ({
  page,
  browser,
}) => {
  await signup(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
  const ws = base.split('/')[2]
  const cmd = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
    return r.json()
  }
  await page.goto(`${base}/site`)
  await page.getByRole('button', { name: 'Use this' }).first().click()
  await expect(page.getByText('Not published yet')).toBeVisible()
  const [site] = await cmd('site.list', {})
  const draft = async () => (await cmd('site.get', { id: site.id })).site.draft_version_id as string

  // A Text block with a known title: the entry the test edits and later changes at the source.
  const added = await cmd('site.edit', {
    id: site.id,
    expected_version_id: await draft(),
    ops: [{ tool: 'add_block', input: { type: 'Text', props: { title: 'Our story', body: 'Since 1998.' } } }],
  })
  const block = added.data.content.find((b: { type: string }) => b.type === 'Text').props.id as string
  await page.reload()

  // Add Hindi in the settings panel.
  await page.getByRole('button', { name: 'Website settings' }).click()
  const panel = page.getByRole('complementary', { name: 'Website settings' })
  await panel.getByLabel('Language to add').selectOption('hi')
  await panel.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(panel.getByText('हिन्दी')).toBeVisible()

  // Review sheet: edit one entry.
  await panel.getByRole('button', { name: 'Review', exact: true }).click()
  const sheet = page.getByRole('dialog', { name: 'Review Hindi' })
  const row = sheet
    .getByRole('listitem')
    .filter({ hasText: /Source: Our story/ })
    .first()
  await row.getByRole('button', { name: 'Edit' }).click()
  await row.getByLabel('Translation, Hindi').fill('हमारी कहानी')
  await row.getByRole('button', { name: 'Save' }).click()
  await expect(row.getByText('हमारी कहानी')).toBeVisible()
  await expect(row.getByText('OK', { exact: true })).toBeVisible()
  await sheet.getByRole('button', { name: 'Mark all as checked' }).click()
  await sheet.getByRole('button', { name: 'Done' }).click()
  // A new language starts hidden until it is checked: show it.
  const show = panel.getByRole('button', { name: 'Show to visitors' })
  if (await show.isVisible()) await show.click()
  await expect(panel.getByRole('button', { name: 'Hide until checked' })).toBeVisible()

  const sub = `e2e-${uniq()}`
  await cmd('site.publish', { id: site.id, expected_version_id: await draft(), subdomain: sub })
  const siteUrl = `http://${sub}.localhost:${port}`
  const visit = async (path: string) => {
    const ctx = await browser.newContext()
    const p = await ctx.newPage()
    const res = await p.goto(`${siteUrl}${path}`)
    return { ctx, p, res }
  }

  const hi = await visit('/hi/')
  expect(hi.res?.status()).toBe(200)
  await expect(hi.p.getByRole('heading', { name: 'हमारी कहानी' })).toBeVisible()
  await expect(hi.p.getByRole('heading', { name: 'Our story' })).toHaveCount(0)
  await expect(hi.p.locator('div[lang=hi]').first()).toBeAttached()
  // The switcher names each language in its own script and links to the same page.
  const nav = hi.p.getByRole('navigation', { name: 'भाषा' }) // the label is in the page's language
  await expect(nav.getByRole('link', { name: 'English' })).toHaveAttribute('href', '/')
  await expect(nav.getByText('हिन्दी')).toBeVisible()
  // hreflang alternates, canonical in the language, and the sitemap lists both addresses.
  await expect(hi.p.locator('link[rel=alternate][hreflang=hi]')).toHaveAttribute('href', `${siteUrl}/hi`)
  await expect(hi.p.locator('link[rel=alternate][hreflang=en]')).toHaveAttribute('href', siteUrl)
  await expect(hi.p.locator('link[rel=canonical]')).toHaveAttribute('href', `${siteUrl}/hi`)
  const map = await (await hi.ctx.request.get(`${siteUrl}/sitemap.xml`)).text()
  expect(map).toContain(`<loc>${siteUrl}/hi</loc>`)
  expect(map).toContain('hreflang="hi"')
  // English stays as it was.
  const en = await visit('/')
  await expect(en.p.getByRole('heading', { name: 'Our story' })).toBeVisible()
  await expect(en.p.getByRole('navigation', { name: 'Language' }).getByText('English')).toBeVisible()
  await en.ctx.close()
  await hi.ctx.close()

  // Change the English source and publish again: the translation no longer matches, so /hi/ shows English.
  const changed = await cmd('site.edit', {
    id: site.id,
    expected_version_id: await draft(),
    ops: [{ tool: 'update_block_props', input: { id: block, props: { title: 'Our journey' } } }],
  })
  await cmd('site.publish', { id: site.id, expected_version_id: changed.site.draft_version_id })
  const again = await visit('/hi/')
  await expect(again.p.getByRole('heading', { name: 'Our journey' })).toBeVisible()
  await expect(again.p.getByText('हमारी कहानी')).toHaveCount(0)
  await again.ctx.close()
})
