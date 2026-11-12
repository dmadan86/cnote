import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('size picker inserts a table; column menu sorts and moves; survives reload', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  const editor = page.getByRole('textbox', { name: 'Page content' })
  await editor.click()
  await page.keyboard.type('/table')
  await page.keyboard.press('Enter')
  await page.getByRole('dialog', { name: 'Table size' }).getByRole('button', { name: '4 × 2 table' }).click()
  await expect(editor.locator('tr')).toHaveCount(4)
  await expect(editor).toBeFocused()
  await expect(editor.locator('tr').first().locator('th')).toHaveCount(2)

  // Header, then three body rows: name | n
  const vals = ['Name', 'n', 'pear', '2', 'apple', '10', 'fig', '1']
  for (const [i, v] of vals.entries()) {
    await page.keyboard.type(v)
    if (i < vals.length - 1) await page.keyboard.press('Tab') // Tab in the last cell would add a row
  }
  const col = (i: number) => editor.locator(`tr > :nth-child(${i})`)
  await col(2).nth(2).click() // a cell in the "n" column
  await page.getByRole('button', { name: 'Column options' }).click()
  await page.getByRole('menuitem', { name: 'Sort decreasing' }).click()
  await expect(col(2)).toHaveText(['n', '10', '2', '1']) // numeric, header stays on top

  await page.getByRole('button', { name: 'Column options' }).click()
  await page.getByRole('menuitem', { name: 'Move column left' }).click()
  await expect(col(1)).toHaveText(['n', '10', '2', '1'])
  await expect(col(2)).toHaveText(['Name', 'apple', 'pear', 'fig'])

  await page.getByRole('button', { name: 'Row options' }).click()
  await page.getByRole('menuitem', { name: 'Move row down' }).click()
  await expect(col(2)).toHaveText(['Name', 'apple', 'fig', 'pear'])

  const saved = page.waitForResponse(
    (r) => r.url().includes('note.update') && !!r.request().postData()?.includes('fig'),
  )
  await saved
  await page.waitForTimeout(2500) // collab gateway snapshot
  await page.reload()
  await expect(col(1)).toHaveText(['n', '10', '1', '2'])
})
