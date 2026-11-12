import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('fixed toolbar: heading, list, bold, undo, persists after reload', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const editor = page.getByRole('textbox', { name: 'Page content' })
  const bar = page.getByRole('toolbar', { name: 'Editor toolbar' })
  await expect(editor).toBeVisible()
  await editor.click()

  const head = `head${uniq()}`
  await page.keyboard.type(head)
  await bar.getByRole('button', { name: /Text style/ }).click()
  await page
    .getByRole('menu', { name: 'Text style' })
    .getByRole('menuitemradio', { name: /Heading 2/ })
    .click()
  await expect(editor.locator('h2', { hasText: head })).toBeVisible()

  // Bold toggles on a selection, undo removes it. Yjs undo merges edits made within 500ms, so pause first.
  await page.waitForTimeout(700)
  for (let i = 0; i < head.length; i++) await page.keyboard.press('Shift+ArrowLeft')
  await bar.getByRole('button', { name: 'Bold' }).click()
  await expect(editor.locator('h2 strong')).toBeVisible()
  await bar.getByRole('button', { name: 'Undo' }).click()
  await expect(editor.locator('h2 strong')).toHaveCount(0)
  await expect(editor.locator('h2', { hasText: head })).toBeVisible()

  await page.keyboard.press('ArrowRight')
  // Cursor moves land on the async selectionchange; Enter on a live selection would replace the heading.
  await expect
    .poll(() =>
      page.evaluate(() => (document.querySelector('.ProseMirror') as any)?.editor?.state.selection.empty),
    )
    .toBe(true)
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter') // leave the heading for a paragraph
  const item = `item${uniq()}`
  await bar.getByRole('button', { name: 'Bulleted list' }).click()
  await expect(editor.locator('ul')).toBeVisible()
  const saved = page.waitForResponse(
    (r) => r.url().includes('note.update') && !!r.request().postData()?.includes(item),
  )
  await page.keyboard.type(item)
  await saved
  await page.waitForTimeout(2500) // let the collab gateway persist its snapshot too

  await page.reload()
  await expect(editor.locator('h2', { hasText: head })).toBeVisible()
  await expect(editor.locator('ul li', { hasText: item })).toBeVisible()
})
