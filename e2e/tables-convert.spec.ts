import { expect, test } from '@playwright/test'
import { gridOf, rowWith, templateBase } from './tables-helpers'

test('a manager converts a number field to currency, and a refused conversion points to a new field', async ({
  page,
}) => {
  await templateBase(page)
  const grid = gridOf(page)
  const row = rowWith(page, 'Steel utensils, 40 sets')

  // a Number field with a value
  await page.getByRole('button', { name: 'Add field' }).click()
  let dlg = page.getByRole('dialog', { name: 'Add field' })
  await dlg.getByLabel('Name').fill('Weight')
  await dlg.getByRole('button', { name: /^Number/ }).click()
  await dlg.getByRole('button', { name: 'Save field' }).click()
  await expect(dlg).toHaveCount(0)
  await row.getByRole('gridcell').nth(9).click() // Weight is the last field
  await page.keyboard.type('250')
  await page.keyboard.press('Enter')
  await expect(row.getByRole('gridcell', { name: '250' })).toBeVisible()

  // number -> currency keeps every value: preview, then convert
  await grid.getByRole('button', { name: 'Field menu: Weight' }).click()
  await page.getByRole('menuitem', { name: 'Edit field' }).click()
  dlg = page.getByRole('dialog', { name: 'Edit field' })
  await dlg.getByRole('button', { name: /^Currency/ }).click()
  await dlg.getByRole('button', { name: 'Save field' }).click()
  await expect(dlg.getByRole('status')).toContainText('0 cleared')
  await dlg.getByRole('button', { name: /^Convert \d+ records/ }).click()
  await expect(dlg).toHaveCount(0)
  await expect(row.getByRole('gridcell', { name: /250\.00/ })).toBeVisible()

  // text -> email with a value that is not an address is refused, and the message points the way out
  await page.getByRole('button', { name: 'Add field' }).click()
  dlg = page.getByRole('dialog', { name: 'Add field' })
  await dlg.getByLabel('Name').fill('Contact')
  await dlg.getByRole('button', { name: /^Single line text/ }).click()
  await dlg.getByRole('button', { name: 'Save field' }).click()
  await expect(dlg).toHaveCount(0)
  await row.getByRole('gridcell').nth(10).click()
  await page.keyboard.type('not an email')
  await page.keyboard.press('Enter')
  await expect(row.getByRole('gridcell', { name: 'not an email' })).toBeVisible()
  await grid.getByRole('button', { name: 'Field menu: Contact' }).click()
  await page.getByRole('menuitem', { name: 'Edit field' }).click()
  dlg = page.getByRole('dialog', { name: 'Edit field' })
  await dlg.getByRole('button', { name: /^Email/ }).click()
  await dlg.getByRole('button', { name: 'Save field' }).click()
  await expect(dlg.getByRole('status')).toContainText('1 cleared')
  await dlg.getByRole('button', { name: /^Convert \d+ records/ }).click()
  await expect(dlg.getByText(/Create a new field and copy the values/i).last()).toBeVisible()
  await expect(dlg).toBeVisible()
})
