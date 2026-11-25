import { expect, test } from '@playwright/test'
import { markdown, newPage, signup } from './helpers'

const PDF = Buffer.from('%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n')
const LINK = /\[price\.pdf\]\(\/api\/w\/[0-9a-f-]{36}\/files\/[0-9a-f-]{36}\)/

test('a PDF attached from the toolbar is a file chip and a plain link, listed in Attachments, and deleting a used file warns', async ({
  page,
}) => {
  const editor = await newPage(page)
  const chooser = page.waitForEvent('filechooser')
  await page
    .getByRole('toolbar', { name: 'Editor toolbar' })
    .getByRole('button', { name: 'Attach file' })
    .click()
  await (await chooser).setFiles({ name: 'price.pdf', mimeType: 'application/pdf', buffer: PDF })
  await expect(editor.locator('.file-chip')).toHaveText('price.pdf')
  await expect.poll(() => markdown(page)).toMatch(LINK)

  const panel = page.getByRole('region', { name: 'Attachments' })
  await expect(panel).toContainText('price.pdf')
  await expect(panel).toContainText(/\d+ B/)
  await expect(panel).toContainText('Tester') // the uploader's name
  await expect(panel.getByRole('link', { name: 'Download price.pdf' })).toHaveAttribute(
    'href',
    /\/api\/w\/.+\/files\//,
  )

  // The text still uses it: the warning says so; declining keeps the file.
  const asked: string[] = []
  page.on('dialog', (d) => {
    asked.push(d.message())
    return asked.length === 1 ? d.dismiss() : d.accept()
  })
  await panel.getByRole('button', { name: 'Delete price.pdf' }).click()
  await expect(panel).toContainText('price.pdf')
  await panel.getByRole('button', { name: 'Delete price.pdf' }).click()
  await expect(panel).not.toContainText('price.pdf')
  expect(asked[0]).toContain('is used in the text')
})

test('a file of a page is not readable by someone who cannot read the page', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  await newPage(a)
  const ws = a.url().split('/w/')[1].split('/')[0]
  const note = a.url().split('/n/')[1]
  const up = await a.request.post(`/api/w/${ws}/files?note_id=${note}&filename=x.pdf`, { data: PDF })
  expect(up.ok()).toBe(true)
  const id = (await up.json()).id
  const b = await (await browser.newContext()).newPage()
  await signup(b)
  expect([403, 404]).toContain((await b.request.get(`/api/w/${ws}/files/${id}`)).status())
  expect((await a.request.get(`/api/w/${ws}/files/${id}`)).status()).toBe(200)
})
