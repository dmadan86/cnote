import { writeFileSync } from 'node:fs'
import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

const FIXTURES =
  '/private/tmp/claude-502/-Users-in45889244-madan-cnote/40c21fff-b1e2-4bc8-80e3-17a69aefd484/scratchpad/fixtures-from-editor.md'

test('colour, alignment, image, emoji, merged + resized table: stored contract strings and reload', async ({
  page,
}) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/([0-9a-f-]+)$/)
  const id = page.url().split('/n/')[1]
  const ws = page.url().match(/\/w\/([0-9a-f-]+)/)?.[1] ?? ''
  const editor = page.getByRole('textbox', { name: 'Page content' })
  const bar = page.getByRole('toolbar', { name: 'Editor toolbar' })
  await editor.click()

  // Centered heading.
  await page.keyboard.type('Head')
  await bar.getByRole('button', { name: /Text style/ }).click()
  await page
    .getByRole('menu', { name: 'Text style' })
    .getByRole('menuitemradio', { name: /Heading 2/ })
    .click()
  await bar.getByRole('button', { name: 'Alignment' }).click()
  await page.getByRole('dialog', { name: 'Alignment' }).getByRole('button', { name: 'Align center' }).click()
  await expect(editor.locator('h2')).toHaveCSS('text-align', 'center')
  await expect(editor).toBeFocused()

  // Emoji (the new paragraph inherits the heading's alignment: reset it).
  await page.keyboard.press('Enter')
  await bar.getByRole('button', { name: 'Alignment' }).click()
  await page.getByRole('dialog', { name: 'Alignment' }).getByRole('button', { name: 'Align left' }).click()
  await expect(editor).toBeFocused()
  await page.keyboard.type('ship ')
  await bar.getByRole('button', { name: 'Emoji' }).click()
  await page.getByRole('dialog', { name: 'Emoji' }).getByRole('button', { name: '🚀' }).click()
  await expect(editor).toContainText('🚀')
  await expect(editor).toBeFocused()

  // Bold + red text. Yjs undo merges edits within 500ms, so pause between steps.
  await page.keyboard.press('Enter')
  await page.keyboard.type('hot')
  await page.waitForTimeout(700)
  for (let i = 0; i < 3; i++) await page.keyboard.press('Shift+ArrowLeft')
  await bar.getByRole('button', { name: 'Bold' }).click()
  await bar.getByRole('button', { name: 'Text colour' }).click()
  await page.getByRole('dialog', { name: 'Text colour' }).getByRole('button', { name: 'Red' }).click()
  await expect(editor.locator('strong span')).toHaveCSS('color', 'rgb(220, 38, 38)')
  await expect(editor).toBeFocused()
  await page.keyboard.press('ArrowRight')
  await expect
    .poll(() =>
      page.evaluate(() => (document.querySelector('.ProseMirror') as any)?.editor?.state.selection.empty),
    )
    .toBe(true)

  // Image by URL (http/https only).
  await page.keyboard.press('Enter')
  await bar.getByRole('button', { name: 'Image' }).click()
  const dlg = page.getByRole('dialog', { name: 'Insert image' })
  await dlg.getByLabel('Image URL').fill('javascript:alert(1)')
  await dlg.getByRole('button', { name: 'Insert image' }).click()
  await expect(dlg.getByRole('alert')).toBeVisible()
  await dlg.getByLabel('Image URL').fill('https://example.com/cat.png')
  await dlg.getByLabel('Alt text').fill('cat')
  await dlg.getByRole('button', { name: 'Insert image' }).click()
  await expect(editor.locator('img[alt="cat"]')).toHaveCount(1)
  await expect(editor).toBeFocused()

  // 2 x 3 table; merge the first two header cells; drag the last column border.
  await page.keyboard.press('Enter')
  await bar.getByRole('button', { name: 'Table' }).click()
  await page.getByRole('dialog', { name: 'Table size' }).getByRole('button', { name: '2 × 3 table' }).click()
  await page.keyboard.type('a')
  await page.keyboard.press('Tab')
  await page.keyboard.type('b')
  await page.keyboard.press('Tab')
  await page.keyboard.type('c')
  await page.waitForTimeout(700)
  await editor.locator('th').first().click()
  await editor
    .locator('th')
    .nth(1)
    .click({ modifiers: ['Shift'] })
  await page.getByRole('toolbar', { name: 'Table' }).getByRole('button', { name: 'Merge cells' }).click()
  await expect(editor.locator('th[colspan="2"]')).toHaveCount(1)
  await expect(
    page.getByRole('toolbar', { name: 'Table' }).getByRole('button', { name: 'Split cell' }),
  ).toBeEnabled()

  const last = (await editor.locator('th').last().boundingBox())!
  await page.mouse.move(last.x + last.width - 2, last.y + last.height / 2)
  await page.mouse.down()
  await page.mouse.move(last.x + last.width + 38, last.y + last.height / 2, { steps: 5 })
  await page.mouse.up()
  await expect(editor.locator('th').last()).toHaveAttribute('colwidth', /\d+/)

  const md = async () => {
    const r = await page.request.post(`/api/w/${ws}/commands/note.get`, { data: { id } })
    const j = await r.json()
    return (j.content_md ?? j.note?.content_md ?? j.data?.content_md ?? JSON.stringify(j)) as string
  }
  await expect.poll(md, { timeout: 15_000 }).toMatch(/colwidth="\d+"/)
  await page.waitForTimeout(3000) // collab gateway snapshot
  // What this editor produces (the Rust side must match it byte for byte).
  const local = await page.evaluate(
    () => (document.querySelector('.ProseMirror') as any).editor.getMarkdown() as string,
  )
  writeFileSync(FIXTURES, `${local}\n`)
  expect(local).toContain('<h2 style="text-align: center">Head</h2>')
  expect(local).toContain('**<span style="color: #dc2626">hot</span>**')
  expect(local).toContain('![cat](https://example.com/cat.png)')
  expect(local).toMatch(
    /<table><tr><th colspan="2"><p>a<\/p><p>b<\/p><\/th><th colwidth="\d+"><p>c<\/p><\/th>/,
  )
  // What the server stored (needs the collab server to run the new md.rs).
  const stored = await md()
  expect(stored).toContain('<h2 style="text-align: center">Head</h2>')
  expect(stored).toContain('**<span style="color: #dc2626">hot</span>**')
  expect(stored).toContain('🚀')
  expect(stored).toContain('![cat](https://example.com/cat.png)')
  expect(stored).toMatch(
    /<table><tr><th colspan="2"><p>a<\/p><p>b<\/p><\/th><th colwidth="\d+"><p>c<\/p><\/th>/,
  )
  expect(stored).toMatch(/<th colspan="2">.*<th colwidth="\d+"><p>c<\/p><\/th><\/tr><tr>/)

  await page.reload()
  const ed = page.getByRole('textbox', { name: 'Page content' })
  await expect(ed.locator('h2')).toHaveCSS('text-align', 'center')
  await expect(ed.locator('strong span')).toHaveCSS('color', 'rgb(220, 38, 38)')
  await expect(ed).toContainText('🚀')
  await expect(ed.locator('img[alt="cat"]')).toHaveCount(1)
  await expect(ed.locator('th[colspan="2"]')).toHaveCount(1)
})

test('pasted HTML cannot smuggle attributes or javascript: links into stored Markdown', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  const editor = page.getByRole('textbox', { name: 'Page content' })
  await editor.click()
  await editor.evaluate((el) => {
    const dt = new DataTransfer()
    dt.setData(
      'text/html',
      '<p style="text-align: center"><span style="color: red&quot; onclick=&quot;x">t</span> <a href="javascript:alert(1)">l</a> <span style="color: #ABC">k</span></p>',
    )
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
  })
  await expect(editor).toContainText('t l k')
  const md = await page.evaluate(
    () => (document.querySelector('.ProseMirror') as any).editor.getMarkdown() as string,
  )
  expect(md).not.toContain('onclick')
  expect(md).not.toContain('javascript:')
  expect(md).toContain('<span style="color: #aabbcc">k</span>')
})
