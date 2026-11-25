import { readFileSync } from 'node:fs'
import { expect, test } from '@playwright/test'
import { markdown, newPage } from './helpers'

// Docs C1 R1: text highlight, cell colour, numbered first column and sticky header row, through a reload and an export.
test('highlight, cell colour, numbered column and sticky header survive a reload and the HTML export', async ({
  page,
}) => {
  const editor = await newPage(page)
  const bar = page.getByRole('toolbar', { name: 'Editor toolbar' })

  // Highlight a word with the toolbar palette (keyboard reachable: the palette takes focus, Escape closes it).
  await page.keyboard.type('Mark the word here')
  await page.waitForTimeout(700)
  for (let i = 0; i < 4; i++) await page.keyboard.press('Shift+ArrowLeft') // "here"
  await bar.getByRole('button', { name: 'Highlight' }).click()
  const palette = page.getByRole('dialog', { name: 'Highlight' })
  await expect(palette.getByRole('button', { name: 'Highlight yellow' })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(palette).toHaveCount(0)
  await bar.getByRole('button', { name: 'Highlight' }).click()
  await palette.getByRole('button', { name: 'Highlight green' }).click()
  const mark = editor.locator('[data-highlight="green"]')
  await expect(mark).toHaveText('here')
  await expect(mark).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  expect(await markdown(page)).toContain('<mark data-color="green">here</mark>')

  // Remove it cleanly, then put it back.
  await bar.getByRole('button', { name: 'Highlight' }).click()
  await palette.getByRole('button', { name: 'No highlight' }).click()
  await expect(editor.locator('[data-highlight]')).toHaveCount(0)
  expect(await markdown(page)).not.toContain('<mark')
  await bar.getByRole('button', { name: 'Highlight' }).click()
  await palette.getByRole('button', { name: 'Highlight green' }).click()
  await expect(mark).toHaveText('here')

  // A tall table so the header row has somewhere to travel.
  await page.evaluate(() => {
    const ed = (document.querySelector('.ProseMirror') as any).editor
    ed.chain()
      .focus('end')
      .insertContent({ type: 'paragraph' })
      .insertTable({ rows: 40, cols: 3, withHeaderRow: true })
      .run()
  })
  const table = editor.locator('table')
  await expect(table).toHaveCount(1)
  await table.locator('th').first().click()
  await page.keyboard.type('Item')
  await table.locator('tr').nth(1).locator('td').first().click()
  await page.keyboard.type('first row')

  // Cell colour from the table toolbar.
  const tools = page.getByRole('toolbar', { name: 'Table' })
  await tools.getByRole('button', { name: 'Cell colour' }).click()
  await page
    .getByRole('dialog', { name: 'Cell colour' })
    .getByRole('button', { name: 'Cell colour red' })
    .click()
  const red = table.locator('td[data-bg="red"]')
  await expect(red).toHaveText('first row')
  await expect(red).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')

  // Numbered first column: numbers are drawn, there are no extra cells.
  const cellsBefore = await table.locator('td, th').count()
  await tools.getByRole('button', { name: 'Numbered' }).click()
  await expect(table).toHaveAttribute('data-numbered', 'true')
  expect(await table.locator('td, th').count()).toBe(cellsBefore)
  // The number is a CSS counter on each body row's first cell (computed content stays "counter(cn-row)", unresolved).
  const pseudo = (loc: import('@playwright/test').Locator) =>
    loc.evaluate((el) => ({
      content: getComputedStyle(el, '::before').content,
      increment: getComputedStyle(el).counterIncrement,
    }))
  expect(await pseudo(table.locator('tr').nth(1).locator('td').first())).toEqual({
    content: 'counter(cn-row)',
    increment: 'cn-row 1',
  })
  expect((await pseudo(table.locator('th').first())).content).toBe('none')
  expect(await table.evaluate((el) => getComputedStyle(el).counterReset)).toBe('cn-row 0')

  // Sticky header: scroll the page until the header would leave the screen; it follows under the toolbar.
  const th = table.locator('th').first()
  await expect(th).not.toHaveClass(/cn-sticky-cell/)
  await table.locator('tr').nth(30).scrollIntoViewIfNeeded()
  await expect(th).toHaveClass(/cn-sticky-cell/)
  const barBottom = (await bar.boundingBox())!.y + (await bar.boundingBox())!.height
  const top = (await th.boundingBox())!.y
  expect(top).toBeGreaterThanOrEqual(barBottom - 2)
  expect(top).toBeLessThan(barBottom + 40)
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.evaluate(() => {
    for (const e of document.querySelectorAll('*')) (e as HTMLElement).scrollTop = 0
  })
  await expect(th).not.toHaveClass(/cn-sticky-cell/)

  // Everything is stored, and a reload keeps it.
  const stored = await markdown(page)
  expect(stored).toContain('<table data-numbered="true">')
  expect(stored).toContain('data-bg="red"')
  expect(stored).toContain('<mark data-color="green">here</mark>')
  await page.waitForResponse(
    (r) => r.url().includes('note.update') && (r.request().postData() ?? '').includes('data-numbered'),
  )
  // Reloading inside the collab fold window (2 s) duplicates the page on main too (reproduced there); let the fold finish.
  await page.waitForTimeout(2500)
  await page.reload()
  const again = page.getByRole('textbox', { name: 'Page content' })
  await expect(again.locator('[data-highlight="green"]')).toHaveText('here')
  await expect(again.locator('table[data-numbered="true"] td[data-bg="red"]')).toHaveText('first row')

  // HTML export carries the tints and the counter rule.
  const dl = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export' }).click()
  await page.getByRole('menuitem', { name: 'Download HTML' }).click()
  const html = readFileSync((await (await dl).path()) as string, 'utf8')
  expect(html).toContain('data-highlight="green"')
  expect(html).toContain('data-bg="red"')
  expect(html).toContain('data-numbered="true"')
  expect(html).toContain('counter-increment:cn-row')
  expect(html).not.toMatch(/<script/i)
})

test('dark mode and a phone width keep the tints readable and the table menu on screen', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' })
  const editor = await newPage(page) // the sidebar that makes a space is a desktop control
  await page.setViewportSize({ width: 390, height: 800 })
  await page.evaluate(() => {
    const ed = (document.querySelector('.ProseMirror') as any).editor
    ed.commands.setContent(
      '<mark data-color="yellow">dark mark</mark>\n\n<table data-numbered="true"><tr><th data-bg="blue"><p>H</p></th></tr><tr><td data-bg="orange"><p>cell</p></td></tr></table>',
      { contentType: 'markdown' },
    )
  })
  const hl = editor.locator('[data-highlight="yellow"]')
  await expect(hl).toHaveText('dark mark')
  // the tint is mixed into the page colours: not the light-mode pale yellow
  const bg = await hl.evaluate((e) => getComputedStyle(e).backgroundColor)
  const [r, g, b] = bg.match(/\d+(\.\d+)?/g)!.map(Number)
  expect(r + g + b).toBeLessThan(450)
  // the sticky toolbar wraps on a phone and covers a click target: put the cursor in the last cell directly
  await page.evaluate(() => {
    const ed = (document.querySelector('.ProseMirror') as any).editor
    let at = 1
    ed.state.doc.descendants((n: any, pos: number) => {
      if (n.isText && n.text === 'cell') at = pos + 1
    })
    ed.chain().focus().setTextSelection(at).run()
  })
  const tools = page.getByRole('toolbar', { name: 'Table' })
  await expect(tools).toBeVisible()
  const box = (await tools.boundingBox())!
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(390)
  await tools.getByRole('button', { name: 'Cell colour' }).click()
  const pal = (await page.getByRole('dialog', { name: 'Cell colour' }).boundingBox())!
  expect(pal.x + pal.width).toBeLessThanOrEqual(390 + 1)
})
