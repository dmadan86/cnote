import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('slash menu inserts a table and to-do, bubble toolbar bolds, all survive reload', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const editor = page.getByRole('textbox', { name: 'Page content' })
  await expect(editor).toBeVisible()
  await editor.click()

  // Selecting text shows the formatting toolbar.
  const word = `loud${uniq()}`
  await page.keyboard.type(word)
  for (let i = 0; i < word.length; i++) await page.keyboard.press('Shift+ArrowLeft')
  await page.getByRole('toolbar', { name: 'Text formatting' }).getByRole('button', { name: 'Bold' }).click()
  await expect(editor.locator('strong', { hasText: word })).toBeVisible()

  // "/" opens the menu; typing filters it; Enter inserts.
  await page.keyboard.press('ArrowRight') // collapse the selection to its end (End is a no-op on macOS)
  // The editor applies cursor moves on the browser's async selectionchange; wait for it before typing on.
  await expect
    .poll(() =>
      page.evaluate(() => (document.querySelector('.ProseMirror') as any)?.editor?.state.selection.empty),
    )
    .toBe(true)
  await page.keyboard.press('Enter')
  await page.keyboard.type('/tab')
  const menu = page.getByRole('listbox', { name: 'Insert block' })
  await expect(menu.getByRole('option', { name: /Table/ })).toBeVisible()
  await page.keyboard.press('Enter')
  await expect(menu).toHaveCount(0)
  await expect(page.getByRole('dialog', { name: 'Table size' })).toBeFocused()
  await page.keyboard.press('Enter') // default 3 × 3
  await expect(editor.locator('table th')).toHaveCount(3)
  const cell = `Cell${uniq()}`
  await page.keyboard.type(cell)
  await expect(page.getByRole('toolbar', { name: 'Table' })).toBeVisible()

  // The empty paragraph Tiptap keeps after a table.
  await editor.locator(':scope > p').last().click()
  await page.keyboard.type('/to-do')
  await page.keyboard.press('Enter')
  const todo = `call${uniq()}`
  const saved = page.waitForResponse(
    (r) => r.url().includes('note.update') && !!r.request().postData()?.includes(todo),
  )
  await page.keyboard.type(todo)
  await saved
  await page.waitForTimeout(2500) // let the collab gateway persist its snapshot too

  await page.reload()
  await expect(editor.locator('table th').first()).toHaveText(cell)
  await expect(editor.locator('strong', { hasText: word })).toBeVisible()
  await expect(editor.locator('ul[data-type="taskList"] li', { hasText: todo })).toBeVisible()
})
