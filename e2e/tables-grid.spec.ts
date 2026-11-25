import { expect, test } from '@playwright/test'
import { gridOf, rowWith, templateBase } from './tables-helpers'

test('grid: add a field, edit by keyboard, paste a block, sort and filter, and it all survives a reload', async ({
  page,
}) => {
  await templateBase(page)
  const grid = gridOf(page)
  await expect(page.getByText('10 records')).toBeVisible()

  // add a Single select field with options
  await page.getByRole('button', { name: 'Add field' }).click()
  const dlg = page.getByRole('dialog', { name: 'Add field' })
  await dlg.getByLabel('Name').fill('Priority')
  await dlg.getByRole('button', { name: /^Single select/ }).click()
  await dlg.getByRole('button', { name: 'Add option' }).click()
  await dlg.getByRole('textbox', { name: 'Option 1' }).fill('Low')
  await dlg.getByRole('button', { name: 'Add option' }).click()
  await dlg.getByRole('textbox', { name: 'Option 2' }).fill('High')
  await dlg.getByRole('button', { name: 'Save field' }).click()
  await expect(dlg).toHaveCount(0)
  await expect(grid.getByRole('columnheader', { name: /Priority/ })).toBeVisible()

  // edit a cell by keyboard: select it, type to replace, Enter to save
  await grid.getByRole('gridcell', { name: 'Steel utensils, 40 sets' }).click()
  await page.keyboard.type('Copper pots, 12 sets')
  await page.keyboard.press('Enter')
  await expect(grid.getByRole('gridcell', { name: 'Copper pots, 12 sets' })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'Steel utensils, 40 sets' })).toHaveCount(0)

  // paste a 2x2 block over Item and Amount of the first two rows (as Excel would put it on the clipboard)
  const first = rowWith(page, 'Copper pots, 12 sets')
  await first.getByRole('gridcell', { name: 'Copper pots, 12 sets' }).click()
  await grid.evaluate((el) => {
    const dt = new DataTransfer()
    dt.setData('text/plain', 'Paste A\t100\nPaste B\t200')
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
  })
  await expect(
    page
      .getByRole('status')
      .filter({ hasText: /Pasted: 2 updated/ })
      .first(),
  ).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'Paste A', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'Paste B', exact: true })).toBeVisible()
  await expect(rowWith(page, 'Paste A')).toContainText('100')
  await expect(rowWith(page, 'Paste B')).toContainText('200')

  // sort by Item, A to Z
  await page.getByRole('button', { name: 'Sort', exact: true }).click()
  const sort = page.getByRole('dialog', { name: 'Sort' })
  await sort.getByRole('button', { name: 'Add sort' }).click()
  await sort.getByLabel('Sort 1 field').selectOption({ label: 'Item' })
  await sort.getByLabel('Sort 1 direction').selectOption('asc')
  await expect(grid.getByRole('row').nth(1)).toContainText('Brass lamps')
  await page.keyboard.press('Escape')

  // filter: Item contains "Paste"
  await page.getByRole('button', { name: 'Filter', exact: true }).click()
  const filter = page.getByRole('dialog', { name: 'Filter' })
  await filter.getByRole('button', { name: 'Add filter' }).click()
  await filter.getByLabel('Field').selectOption({ label: 'Item' })
  // view edits show at once and are saved a moment later: wait for the save before a reload is a fair test
  const saved = page.waitForResponse(
    (r) =>
      r.url().includes('table_view.update') && r.ok() && (r.request().postData() ?? '').includes('Paste'),
  )
  await filter.getByLabel('Value for Item').fill('Paste')
  await expect(page.getByText('2 records', { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')

  await saved
  // the view is shared and saved: a reload keeps the sort, the filter and the data
  await page.reload()
  await expect(page.getByText('2 records', { exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'Paste A', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'Copper pots, 12 sets' })).toHaveCount(0)
  await page.getByRole('button', { name: 'Sort', exact: true }).click()
  await expect(page.getByLabel('Sort 1 field')).toHaveValue(/.+/)
  await expect(page.getByLabel('Sort 1 field').locator('option:checked')).toHaveText('Item')
  await page.keyboard.press('Escape')
  await expect(grid.getByRole('columnheader', { name: /Priority/ })).toBeVisible()
  await page.getByRole('button', { name: 'Clear filters' }).first().click()
  await expect(page.getByText('10 records')).toBeVisible()
})
