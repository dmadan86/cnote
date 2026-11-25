import { readFileSync } from 'node:fs'
import { expect, test } from '@playwright/test'
import { newPage } from './helpers'

// Docs A2 export (docs-parity 7.9, 7.10): client-side, one page, built from the live editor.
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
)
const MD = [
  '> [!WARNING]',
  '> Keep the **vault** key safe.',
  '',
  '<details><summary>Returns policy</summary>',
  '',
  'Seven days.',
  '',
  '</details>',
  '',
  'Order <span data-status="green">DONE</span>.',
  '',
  '[[toc]]',
  '',
  '# Policy',
  '',
  '```ts',
  'const a = 1',
  '```',
].join('\n')

async function setup(page: import('@playwright/test').Page) {
  const editor = await newPage(page)
  await page.getByRole('toolbar', { name: 'Editor toolbar' }).getByRole('button', { name: 'Image' }).click()
  const chooser = page.waitForEvent('filechooser')
  await page
    .getByRole('dialog', { name: 'Insert image' })
    .getByRole('button', { name: 'Upload from your device' })
    .click()
  await (await chooser).setFiles({ name: 'bill.png', mimeType: 'image/png', buffer: PNG })
  await expect(editor.locator('img[src^="/api/w/"]')).toHaveCount(1)
  await page.evaluate((md) => {
    const ed = (document.querySelector('.ProseMirror') as any).editor
    ed.commands.insertContentAt(ed.state.doc.content.size, md, { contentType: 'markdown' })
  }, MD)
  await expect(editor.getByRole('note', { name: 'Warning panel' })).toBeVisible()
}

const menu = async (page: import('@playwright/test').Page, item: string) => {
  await page.getByRole('button', { name: 'Export' }).click()
  await page.getByRole('menuitem', { name: item }).click()
}

test('Markdown and HTML downloads carry the page with the image embedded', async ({ page }) => {
  await setup(page)

  const mdDl = page.waitForEvent('download')
  await menu(page, 'Download Markdown')
  const md = readFileSync((await (await mdDl).path()) as string, 'utf8')
  expect(md).toContain('> [!WARNING]')
  expect(md).toContain('<details><summary>Returns policy</summary>')
  expect(md).toMatch(/!\[[^\]]*\]\(data:image\/png;base64,/)
  expect(md).not.toContain('/api/w/')

  const htmlDl = page.waitForEvent('download')
  await menu(page, 'Download HTML')
  const d = await htmlDl
  expect(d.suggestedFilename()).toMatch(/\.html$/)
  const html = readFileSync((await d.path()) as string, 'utf8')
  expect(html).toContain('<!doctype html>')
  expect(html).toMatch(/<img [^>]*src="data:image\/png;base64,/)
  expect(html).toMatch(/<details[^>]* open/)
  expect(html).toContain('Warning:')
  expect(html).toContain('language-ts')
  expect(html).toMatch(/<nav[^>]*data-toc[^>]*>[\s\S]*href="#section-1"/)
  expect(html).not.toMatch(/<script/i)
  await expect(page.getByRole('status').filter({ hasText: 'Export ready' })).toHaveCount(1)
})

test('print mode shows only the resolved page, with expands open and the TOC as links', async ({ page }) => {
  await setup(page)
  await page.evaluate(() => {
    ;(window as any).printed = 0
    window.print = () => {
      ;(window as any).printed++
    }
  })
  await menu(page, 'Export as PDF')
  await expect.poll(() => page.evaluate(() => (window as any).printed)).toBe(1)

  await page.emulateMedia({ media: 'print' })
  const root = page.locator('#print-root')
  await expect(root).toBeVisible()
  await expect(root.locator('details[open] summary', { hasText: 'Returns policy' })).toBeVisible()
  await expect(root.locator('nav[data-toc] a[href="#section-1"]')).toHaveText('Policy')
  await expect(root.locator('img[src^="data:image/png"]')).toHaveCount(1)
  await expect(page.getByRole('textbox', { name: 'Page content' })).toBeHidden()
  await expect(page.getByRole('button', { name: 'Export' })).toBeHidden()
})
