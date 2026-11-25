import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

const PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='

test('a public page link: create, open signed out at phone width, turn off', async ({ page, browser }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const call = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBe(true)
    return r.json()
  }
  const space = await call('space.create', {
    key: `P${uniq().slice(0, 4)}`,
    name: 'Public space',
    visibility: 'open',
  })
  const title = `Shared plan ${uniq()}`
  const note = await call('note.create', { space_id: space.id, title, content_md: 'placeholder' })
  const file = await call('file.upload', { note_id: note.id, filename: 'dot.png', data_base64: PNG })
  await call('note.update', {
    id: note.id,
    schema: 2,
    expected_epoch: 1,
    content_md: `Hello readers\n\n![A dot](/api/w/${ws}/files/${file.id})\n\nSee [the docs](https://example.com/docs) and [@Someone](/w/${ws}/people/${note.id}).`,
  })
  await call('comment.add', { note_id: note.id, body: 'internal-only remark' })

  await page.goto(`/w/${ws}/n/${note.id}`)
  await page.getByRole('button', { name: 'Share', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Share page' })
  const section = dialog.getByRole('region', { name: 'Share publicly' })
  await section.getByRole('button', { name: 'Create public link' }).click()
  await expect(section.getByText('This link is shown only once.')).toBeVisible()
  const url = await section.getByLabel(/Public link/).inputValue()
  expect(url).toMatch(/\/pg\/[A-Za-z0-9_-]{43}$/)
  await expect(section.getByRole('img', { name: 'QR code of the public link' })).toBeVisible()
  await expect(section.getByRole('button', { name: 'Create new link' })).toBeVisible()
  await dialog.getByRole('button', { name: 'Done' }).click()
  // the header says so, to anyone who reads the page
  await expect(page.getByText('Public', { exact: true })).toBeVisible()

  // a signed-out phone
  const anon = await browser.newContext({ viewport: { width: 390, height: 800 } })
  const p = await anon.newPage()
  const path = new URL(url).pathname
  const res = await p.goto(path)
  expect(res?.headers()['referrer-policy']).toBe('no-referrer')
  expect(res?.headers()['x-robots-tag']).toContain('noindex')
  await expect(p.getByRole('heading', { name: title })).toBeVisible()
  await expect(p.getByText('Hello readers')).toBeVisible()
  await expect(p.getByText('Shared with C Note')).toBeVisible()
  await expect(p.getByRole('link', { name: 'the docs' })).toHaveAttribute('rel', /nofollow/)
  await expect(p.getByText('@Someone')).toBeVisible()
  await expect(p.getByText('internal-only remark')).toHaveCount(0)
  await expect(p.locator('meta[name="robots"]')).toHaveAttribute('content', /noindex/)
  await expect(p.getByRole('navigation')).toHaveCount(0) // no app chrome
  const img = p.getByRole('img', { name: 'A dot' })
  await expect(img).toBeVisible()
  expect(await img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0)
  expect(await img.getAttribute('src')).toMatch(
    new RegExp(`^/api/public/page/[A-Za-z0-9_-]{43}/files/${file.id}$`),
  )
  expect(await p.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)

  // turn it off: the same address now says unavailable
  await page.getByRole('button', { name: 'Share', exact: true }).click()
  await page.getByRole('dialog', { name: 'Share page' }).getByRole('button', { name: 'Turn off' }).click()
  await expect(
    page.getByRole('dialog', { name: 'Share page' }).getByRole('button', { name: 'Create public link' }),
  ).toBeVisible()
  await p.reload()
  await expect(p.getByText('This page is not available.')).toBeVisible()
  await expect(p.getByRole('img', { name: 'A dot' })).toHaveCount(0)
  await anon.close()
})

test('a replaced link stops the old address at once', async ({ page, request }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const call = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBe(true)
    return r.json()
  }
  const space = await call('space.create', {
    key: `Q${uniq().slice(0, 4)}`,
    name: 'Q space',
    visibility: 'open',
  })
  const note = await call('note.create', { space_id: space.id, title: 'Replace me', content_md: 'x' })
  const a = (await call('page_link.create', { note_id: note.id })).url as string
  const b = (await call('page_link.create', { note_id: note.id })).url as string
  const api = (u: string) => `/api/public/page/${new URL(u).pathname.split('/').pop()}`
  expect((await request.get(api(a))).status()).toBe(404)
  expect((await request.get(api(b))).status()).toBe(200)
})
