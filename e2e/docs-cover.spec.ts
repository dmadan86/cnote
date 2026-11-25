import { readFileSync } from 'node:fs'
import { expect, type Page, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Docs C1 R2 (docs-parity-c 5.3, 13.6): the page cover. Upload or a built-in gradient, reposition, remove; public page; export; archive.
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
)

async function setup(page: Page) {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const call = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBe(true)
    return r.json()
  }
  const space = await call('space.create', {
    key: `C${uniq().slice(0, 4)}`,
    name: 'Cover space',
    visibility: 'open',
  })
  const title = `Covered page ${uniq()}`
  const note = await call('note.create', { space_id: space.id, title, content_md: 'Body text' })
  await page.goto(`/w/${ws}/n/${note.id}`)
  await expect(page.getByLabel('Page title')).toHaveValue(title)
  const cover = async () =>
    (await call('note.get', { id: note.id })).cover as { file_id: string; y: number } | null
  return { ws, call, note, title, cover }
}

test('add a built-in cover, reposition it by drag and by keyboard, change it by upload, remove it', async ({
  page,
}) => {
  const { cover, title } = await setup(page)
  await expect(page.getByTestId('page-cover')).toHaveCount(0)

  await page.getByRole('button', { name: 'Add cover' }).click()
  await page.getByRole('button', { name: 'Ocean gradient' }).click()
  const band = page.getByTestId('page-cover')
  await expect(band).toBeVisible()
  // the alt text comes from the title
  const img = band.getByRole('img', { name: `Cover of ${title}` })
  await expect.poll(() => img.evaluate((i: HTMLImageElement) => i.naturalWidth)).toBeGreaterThan(0)
  await expect.poll(async () => (await cover())?.y).toBe(50)

  // reposition by dragging up, then cancel keeps the old position
  await band.hover()
  await band.getByRole('button', { name: 'Reposition' }).click()
  const slider = band.getByRole('slider', { name: 'Cover position' })
  const box = (await slider.boundingBox()) as { x: number; y: number; width: number; height: number }
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2 - box.height / 4, { steps: 4 })
  await page.mouse.up()
  await band.getByRole('button', { name: 'Cancel' }).click()
  expect((await cover())?.y).toBe(50)

  // and by keyboard, then save
  await band.hover()
  await band.getByRole('button', { name: 'Reposition' }).click()
  await slider.focus()
  for (let i = 0; i < 4; i++) await page.keyboard.press('ArrowDown')
  await expect(slider).toHaveAttribute('aria-valuenow', '70')
  await band.getByRole('button', { name: 'Save position' }).click()
  await expect.poll(async () => (await cover())?.y).toBe(70)
  await expect(img).toHaveCSS('object-position', /70%/)

  // change to an uploaded image
  await band.hover()
  await band.getByRole('button', { name: 'Change' }).click()
  await page
    .getByTestId('cover-file')
    .setInputFiles({ name: 'cover.png', mimeType: 'image/png', buffer: PNG })
  await expect.poll(async () => (await cover())?.y).toBe(50)
  await page.reload()
  await expect(page.getByTestId('page-cover').getByRole('img')).toBeVisible()

  // remove
  await page.getByTestId('page-cover').hover()
  await page.getByRole('button', { name: 'Remove' }).click()
  await expect(page.getByTestId('page-cover')).toHaveCount(0)
  await expect.poll(cover).toBeNull()
  await expect(page.getByRole('button', { name: 'Add cover' })).toBeVisible()
})

test('a cover works at phone width, with its buttons shown without hover', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 740 })
  const { call, note } = await setup(page)
  const file = await call('file.upload', {
    note_id: note.id,
    filename: 'c.png',
    data_base64: PNG.toString('base64'),
  })
  await call('note.cover_set', { note_id: note.id, file_id: file.id, y: 40 })
  await page.reload()
  // the details panel is 320 px wide and would fill a phone; the page itself is what this checks
  await page.getByRole('button', { name: 'Toggle details panel' }).dispatchEvent('click')
  const band = page.getByTestId('page-cover')
  await expect(band).toBeVisible()
  const box = (await band.boundingBox()) as { width: number; height: number }
  expect(box.width).toBeLessThanOrEqual(375)
  expect(box.height).toBeLessThan(200)
  await expect(band.getByRole('button', { name: 'Change' })).toBeVisible()
  await expect(band.getByRole('button', { name: 'Remove' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})

test('the cover shows on the public page and in the HTML export', async ({ page, browser }) => {
  const { ws, call, note, title } = await setup(page)
  const file = await call('file.upload', {
    note_id: note.id,
    filename: 'c.png',
    data_base64: PNG.toString('base64'),
  })
  await call('note.cover_set', { note_id: note.id, file_id: file.id, y: 20 })
  await page.reload()

  const dl = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export' }).click()
  await page.getByRole('menuitem', { name: 'Download HTML' }).click()
  const html = readFileSync((await (await dl).path()) as string, 'utf8')
  expect(html).toMatch(/<img [^>]*class="cn-cover"[^>]*src="data:image\/png;base64,/)
  expect(html).toContain('object-position: 50% 20%')

  await page.getByRole('button', { name: 'Share', exact: true }).click()
  const section = page
    .getByRole('dialog', { name: 'Share page' })
    .getByRole('region', { name: 'Share publicly' })
  await section.getByRole('button', { name: 'Create public link' }).click()
  const url = await section.getByLabel(/Public link/).inputValue()
  const anon = await browser.newContext({ viewport: { width: 390, height: 800 } })
  const p = await anon.newPage()
  await p.goto(new URL(url).pathname)
  const img = p.getByRole('img', { name: `Cover of ${title}` })
  await expect(img).toBeVisible()
  await expect.poll(() => img.evaluate((i: HTMLImageElement) => i.naturalWidth)).toBeGreaterThan(0)
  await expect(img).toHaveCSS('object-position', /20%/)
  // another file of the page is not served by the public route
  const other = await call('file.upload', {
    note_id: note.id,
    filename: 'o.png',
    data_base64: PNG.toString('base64'),
  })
  const res = await p.request.get(
    `${new URL(url).pathname.replace('/pg/', '/api/public/page/')}/files/${other.id}`,
  )
  expect(res.status()).toBe(404)
  await anon.close()
  expect(ws).toBeTruthy()
})

test('an archived page keeps its cover but cannot change it', async ({ page }) => {
  const { ws, call, note, cover } = await setup(page)
  const file = await call('file.upload', {
    note_id: note.id,
    filename: 'c.png',
    data_base64: PNG.toString('base64'),
  })
  await call('note.cover_set', { note_id: note.id, file_id: file.id })
  await call('doc.archive', { id: note.id })
  await page.goto(`/w/${ws}/n/${note.id}`)
  await expect(page.getByText('Archived. This page is read-only')).toBeVisible()
  const band = page.getByTestId('page-cover')
  await expect(band).toBeVisible()
  for (const name of ['Change', 'Reposition', 'Remove', 'Add cover'])
    await expect(page.getByRole('button', { name })).toHaveCount(0)
  const res = await page.request.post(`/api/w/${ws}/commands/note.cover_set`, {
    data: { note_id: note.id, file_id: null },
  })
  expect(res.status()).toBe(403)
  expect((await cover())?.file_id).toBe(file.id)
})
