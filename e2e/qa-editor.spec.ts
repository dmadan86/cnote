import { expect, test } from '@playwright/test'
import { markdown, newPage, uniq } from './helpers'

const MOD = process.platform === 'darwin' ? 'Meta' : 'Control'

test('ED-002 H1 to H6 via Markdown shortcuts', async ({ page }) => {
  const editor = await newPage(page)
  for (let n = 1; n <= 6; n++) {
    await page.keyboard.type(`${'#'.repeat(n)} h${n}`)
    await page.keyboard.press('Enter')
  }
  for (let n = 1; n <= 6; n++) await expect(editor.locator(`h${n}`, { hasText: `h${n}` })).toHaveCount(1)
  const md = await markdown(page)
  for (let n = 1; n <= 6; n++) expect(md).toContain(`${'#'.repeat(n)} h${n}`)
})

test('ED-003 italic, link and ordered list', async ({ page }) => {
  const editor = await newPage(page)
  await page.keyboard.type('slanted')
  await page.keyboard.press(`${MOD}+a`)
  await page.keyboard.press(`${MOD}+i`)
  await expect(editor.locator('em', { hasText: 'slanted' })).toBeVisible()
  await page.keyboard.press('ArrowRight')
  // Cursor moves land on the async selectionchange; Enter on a live selection would replace the text.
  await expect
    .poll(() =>
      page.evaluate(() => (document.querySelector('.ProseMirror') as any)?.editor?.state.selection.empty),
    )
    .toBe(true)
  await page.keyboard.press('Enter')
  await page.keyboard.type('1. first')
  await page.keyboard.press('Enter')
  await page.keyboard.type('second')
  await expect(editor.locator('ol li')).toHaveCount(2)
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter')
  await page.keyboard.type('docs')
  page.once('dialog', (d) => d.accept('https://example.test/a'))
  for (let i = 0; i < 4; i++) await page.keyboard.press('Shift+ArrowLeft')
  await page
    .getByRole('toolbar', { name: 'Text formatting' })
    .getByRole('button', { name: 'Link', exact: true })
    .click()
  await expect(editor.locator('a[href="https://example.test/a"]')).toHaveText('docs')
  // javascript: links are refused
  page.once('dialog', (d) => d.accept('javascript:alert(1)'))
  await page
    .getByRole('toolbar', { name: 'Text formatting' })
    .getByRole('button', { name: 'Link', exact: true })
    .click()
  await expect(editor.locator('a[href^="javascript"]')).toHaveCount(0)
  const md = await markdown(page)
  expect(md).toContain('1. first')
  expect(md).toContain('[docs](https://example.test/a)')
  expect(md).toMatch(/[*_]slanted[*_]/)
})

test('ED-005 ED-006 slash menu filters, navigates with arrows and closes on Escape', async ({ page }) => {
  const editor = await newPage(page)
  await page.keyboard.type('/')
  const menu = page.getByRole('listbox', { name: 'Insert block' })
  const all = await menu.getByRole('option').count()
  expect(all).toBeGreaterThan(3)
  await page.keyboard.type('code')
  await expect(menu.getByRole('option', { name: /Code/ })).toBeVisible()
  await expect(menu.getByRole('option', { name: /Heading 1/ })).toHaveCount(0)
  expect(await menu.getByRole('option').count()).toBeLessThan(all)
  await page.keyboard.press('Backspace')
  await page.keyboard.press('Backspace')
  await page.keyboard.press('Backspace')
  await page.keyboard.press('Backspace')
  const options = menu.getByRole('option')
  await expect(options.first()).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('ArrowDown')
  await expect(options.nth(1)).toHaveAttribute('aria-selected', 'true')
  await expect(options.first()).not.toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('ArrowUp')
  await expect(options.first()).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('Escape')
  await expect(menu).toHaveCount(0)
  await expect(editor).toBeFocused()
})

test('ED-008 add and remove table rows and columns', async ({ page }) => {
  const editor = await newPage(page)
  await page.keyboard.type('/table')
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter') // 3 x 3
  await expect(editor.locator('table tr')).toHaveCount(3)
  await page.keyboard.type('x')
  const bar = page.getByRole('toolbar', { name: 'Table' })
  await expect(bar).toBeVisible()
  await editor.locator('td, th').first().hover()
  await page.getByRole('button', { name: 'Row options' }).click()
  await page.getByRole('menuitem', { name: 'Add row below' }).click()
  await expect(editor.locator('table tr')).toHaveCount(4)
  await editor.locator('td, th').first().hover()
  await page.getByRole('button', { name: 'Column options' }).click()
  await page.getByRole('menuitem', { name: 'Add column right' }).click()
  await expect(editor.locator('table tr').first().locator('th, td')).toHaveCount(4)
  await editor.locator('td, th').first().hover()
  await page.getByRole('button', { name: 'Column options' }).click()
  await page.getByRole('menuitem', { name: 'Delete column' }).click()
  await expect(editor.locator('table tr').first().locator('th, td')).toHaveCount(3)
  await editor.locator('td, th').first().hover()
  await page.getByRole('button', { name: 'Row options' }).click()
  await page.getByRole('menuitem', { name: 'Delete row' }).click()
  await expect(editor.locator('table tr')).toHaveCount(3)
})

test('ED-009 pasted external table keeps structure and loses scripts and handlers', async ({ page }) => {
  const editor = await newPage(page)
  await editor.evaluate((el) => {
    const dt = new DataTransfer()
    dt.setData(
      'text/html',
      '<table onclick="x()"><tr><th>A</th><th>B</th></tr><tr><td onmouseover="y()">1</td><td><script>alert(1)</script>2</td></tr></table>',
    )
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
  })
  await expect(editor.locator('table tr')).toHaveCount(2)
  await expect(editor.locator('th')).toHaveCount(2)
  await expect(editor.locator('td')).toHaveCount(2)
  expect(await editor.locator('table').evaluate((t) => t.outerHTML)).not.toMatch(
    /onclick|onmouseover|<script/,
  )
  expect(await markdown(page)).not.toMatch(/onclick|onmouseover|<script|alert\(1\)/)
})

test('ED-010 code block keeps whitespace and language, and survives reload', async ({ page }) => {
  const editor = await newPage(page)
  await page.keyboard.type('```js ')
  await page.keyboard.type('if (a) {')
  await page.keyboard.press('Enter')
  await page.keyboard.type('    b()')
  await expect(editor.locator('pre code')).toContainText('    b()')
  await expect.poll(() => markdown(page)).toContain('```js')
  expect(await markdown(page)).toContain('    b()')
  const saved = page.waitForResponse((r) => r.url().includes('note.update') && r.ok())
  await page.keyboard.type('}x')
  await saved
  await page.waitForTimeout(2500)
  await page.reload()
  await expect(editor.locator('pre code')).toContainText('    b()')
})

test('ED-014 redo restores what undo removed', async ({ page }) => {
  const editor = await newPage(page)
  await page.keyboard.type(`keep${uniq()}`)
  await page.waitForTimeout(700) // Yjs undo merges edits within 500ms
  await page.keyboard.type(' dropped')
  await page.keyboard.press(`${MOD}+z`)
  await expect(editor).not.toContainText('dropped')
  await page.keyboard.press(`${MOD}+Shift+z`)
  await expect(editor).toContainText('dropped')
})

test('ED-015 copy between documents keeps formatting', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const editor = await newPage(page)
  await page.keyboard.type('## Copied heading')
  await page.keyboard.press(`${MOD}+a`)
  await page.keyboard.press(`${MOD}+c`)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(editor.locator('h2')).toHaveCount(0)
  await editor.click()
  await page.keyboard.press(`${MOD}+v`)
  await expect(editor.locator('h2', { hasText: 'Copied heading' })).toBeVisible()
})

test('ED-017 content the editor has no node for opens safely', async ({ page }) => {
  const editor = await newPage(page)
  const m = page.url().match(/\/w\/([0-9a-f-]+)\/n\//)!
  const api = process.env.API_URL ?? 'http://localhost:4000'
  const body =
    'before\n\n<custom-widget data-x="1">inner</custom-widget>\n\n```mermaid-unknown\na-->b\n```\n\nafter'
  const sp = (
    await (await page.request.post(`${api}/api/w/${m[1]}/commands/space.list`, { data: {} })).json()
  )[0].id
  const r = await page.request.post(`${api}/api/w/${m[1]}/commands/note.create`, {
    data: { space_id: sp, title: 'Odd', content_md: body },
  })
  expect(r.ok()).toBeTruthy()
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  await page.goto(`/w/${m[1]}/n/${(await r.json()).id}`)
  await expect(editor).toContainText('before')
  await expect(editor).toContainText('after')
  await expect(editor).toContainText('inner')
  expect(errors).toEqual([])
})
