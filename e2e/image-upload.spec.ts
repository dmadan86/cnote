import { expect, test } from '@playwright/test'
import { markdown, newPage } from './helpers'

const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
)
const STORED = /!\[[^\]]*\]\(\/api\/w\/[0-9a-f-]{36}\/files\/[0-9a-f-]{36}\)/

test('an image uploaded from the dialog and one pasted are stored as the same-origin files URL', async ({
  page,
}) => {
  const editor = await newPage(page)
  await page.getByRole('toolbar', { name: 'Editor toolbar' }).getByRole('button', { name: 'Image' }).click()
  const chooser = page.waitForEvent('filechooser')
  await page
    .getByRole('dialog', { name: 'Insert image' })
    .getByRole('button', { name: 'Upload from your device' })
    .click()
  await (await chooser).setFiles({ name: 'bill.png', mimeType: 'image/png', buffer: PNG })
  await expect(editor.locator('img[src^="/api/w/"]')).toHaveCount(1)
  await expect.poll(() => markdown(page)).toMatch(STORED)

  // Paste: a clipboard event carrying an image file.
  await editor.click()
  await page.evaluate((b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
    const dt = new DataTransfer()
    dt.items.add(new File([bytes], 'shot.png', { type: 'image/png' }))
    document
      .querySelector('.ProseMirror')
      ?.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
  }, PNG.toString('base64'))
  await expect(editor.locator('img[src^="/api/w/"]')).toHaveCount(2)

  // It survives a reload (the stored URL serves the bytes to a signed-in reader).
  await expect.poll(() => markdown(page)).toMatch(STORED)
  await page.waitForTimeout(1200)
  await page.reload()
  const img = page.getByRole('textbox', { name: 'Page content' }).locator('img[src^="/api/w/"]').first()
  await expect(img).toBeVisible()
  await expect.poll(() => img.evaluate((i: HTMLImageElement) => i.naturalWidth)).toBeGreaterThan(0)
})
