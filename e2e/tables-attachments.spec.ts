import { expect, type Page, test } from '@playwright/test'
import { rowWith, templateBase, wsOf } from './tables-helpers'

// 1x1 PNG
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64',
)
const PDF = Buffer.from('%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n')

/** Adds an attachment field to the first table of the base the page shows (the field dialog does not offer it yet). */
async function addAttachmentField(page: Page, name: string) {
  const ws = wsOf(page).replace('/w/', '')
  const list = await (await page.request.post(`/api/w/${ws}/commands/base.list`, { data: {} })).json()
  const base = list.find((b: { id: string }) => page.url().includes(b.id))
  const detail = await (
    await page.request.post(`/api/w/${ws}/commands/base.get`, { data: { id: base.id } })
  ).json()
  const r = await page.request.post(`/api/w/${ws}/commands/field.create`, {
    data: { table_id: detail.tables[0].id, name, type: 'attachment' },
  })
  expect(r.ok(), await r.text()).toBe(true)
  await page.reload()
}

const openRecord = async (page: Page) => {
  await rowWith(page, 'Steel utensils, 40 sets')
    .getByRole('button', { name: /^Open record/ })
    .click()
  return page.getByRole('dialog', { name: /^Record / })
}

test('attach an image and a PDF, reload and see both; other types are refused in plain words', async ({
  page,
}) => {
  await templateBase(page)
  await addAttachmentField(page, 'Documents')

  let panel = await openRecord(page)
  const files = panel.getByRole('list', { name: 'Files in Documents' })
  const input = panel.locator('input[type=file]')

  await input.setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('just text') })
  await expect(panel.getByRole('alert')).toContainText('"notes.txt" was not added')
  await expect(panel.getByRole('alert')).toContainText('Only pictures')

  await input.setInputFiles([
    { name: 'photo.png', mimeType: 'image/png', buffer: PNG },
    { name: 'invoice.pdf', mimeType: 'application/pdf', buffer: PDF },
  ])
  await expect(files.getByRole('link', { name: 'photo.png' })).toBeVisible()
  await expect(files.getByRole('link', { name: 'invoice.pdf' })).toBeVisible()
  await expect(files.getByRole('img', { name: 'photo.png' })).toBeVisible()

  await panel.getByRole('button', { name: 'Close record' }).click()
  await page.reload()
  panel = await openRecord(page)
  await expect(panel.getByRole('link', { name: 'photo.png' })).toBeVisible()
  await expect(panel.getByRole('link', { name: 'invoice.pdf' })).toBeVisible()

  // remove with Undo, then remove for real (closing the panel saves it)
  await panel.getByRole('button', { name: 'Remove invoice.pdf' }).click()
  await expect(panel.getByRole('link', { name: 'invoice.pdf' })).toHaveCount(0)
  await panel.getByRole('button', { name: 'Undo' }).click()
  await expect(panel.getByRole('link', { name: 'invoice.pdf' })).toBeVisible()
  await panel.getByRole('button', { name: 'Remove invoice.pdf' }).click()
  await panel.getByRole('button', { name: 'Close record' }).click()
  await expect(panel).toHaveCount(0)
  panel = await openRecord(page)
  await expect(panel.getByRole('link', { name: 'photo.png' })).toBeVisible()
  await expect(panel.getByRole('link', { name: 'invoice.pdf' })).toHaveCount(0)
})

const UPDATE = '**/commands/record.update'

test('closing the panel waits for an upload and its save; a failed save keeps the panel open and the files for Try again', async ({
  page,
}) => {
  await templateBase(page)
  await addAttachmentField(page, 'Documents')
  let panel = await openRecord(page)

  // slow save: Close waits for it, then the file is there on reopening
  await page.route(UPDATE, async (route) => {
    await new Promise((r) => setTimeout(r, 800))
    await route.continue()
  })
  await panel
    .locator('input[type=file]')
    .setInputFiles({ name: 'slow.png', mimeType: 'image/png', buffer: PNG })
  await panel.getByRole('button', { name: 'Close record' }).click()
  await expect(panel).toHaveCount(0)
  await page.unroute(UPDATE)
  panel = await openRecord(page)
  await expect(panel.getByRole('link', { name: 'slow.png' })).toBeVisible()

  // the server is down (5xx): the panel stays open with the reason; the staged file survives for Try again
  let down = true
  await page.route(UPDATE, async (route) => {
    if (down)
      await route.fulfill({ status: 503, json: { error: { code: 'internal', message: 'Try later' } } })
    else await route.continue()
  })
  await panel
    .locator('input[type=file]')
    .setInputFiles({ name: 'keep.png', mimeType: 'image/png', buffer: PNG })
  await expect(panel.getByRole('button', { name: 'Try again' })).toBeVisible()
  await panel.getByRole('button', { name: 'Close record' }).click()
  await expect(panel).toBeVisible()
  await expect(panel.getByRole('alert').first()).toBeVisible()
  down = false
  await panel.getByRole('button', { name: 'Try again' }).click()
  await expect(panel.getByRole('link', { name: 'keep.png' })).toBeVisible()
  await expect(panel.getByRole('button', { name: 'Try again' })).toHaveCount(0)

  // a definite rejection (4xx): nothing to retry
  await page.unroute(UPDATE)
  await page.route(UPDATE, (route) =>
    route.fulfill({ status: 409, json: { error: { code: 'conflict', message: 'No room' } } }),
  )
  await panel
    .locator('input[type=file]')
    .setInputFiles({ name: 'no.png', mimeType: 'image/png', buffer: PNG })
  await expect(panel.getByRole('alert').filter({ hasText: 'No room' })).toBeVisible()
  await expect(panel.getByRole('button', { name: 'Try again' })).toHaveCount(0)
})

test('attachment writes of one record never overlap, so the last response shown is the newest', async ({
  page,
}) => {
  await templateBase(page)
  await addAttachmentField(page, 'Documents')
  const panel = await openRecord(page)
  const input = panel.locator('input[type=file]')
  await input.setInputFiles({ name: 'old.png', mimeType: 'image/png', buffer: PNG })
  await expect(panel.getByRole('link', { name: 'old.png' })).toBeVisible()

  let live = 0
  let peak = 0
  await page.route(UPDATE, async (route) => {
    peak = Math.max(peak, ++live)
    await new Promise((r) => setTimeout(r, 600))
    await route.continue()
    live--
  })
  await input.setInputFiles({ name: 'new.png', mimeType: 'image/png', buffer: PNG })
  await panel.getByRole('button', { name: 'Remove old.png' }).click()
  await panel.getByRole('button', { name: 'Close record' }).click()
  await expect(panel).toHaveCount(0)
  expect(peak).toBe(1)
  await page.unroute(UPDATE)
  const again = await openRecord(page)
  await expect(again.getByRole('link', { name: 'new.png' })).toBeVisible()
  await expect(again.getByRole('link', { name: 'old.png' })).toHaveCount(0)
})
