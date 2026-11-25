import { expect, test } from '@playwright/test'
import { gridOf, rowWith, templateBase } from './tables-helpers'

test('linked records: pick a customer in the link picker, then see the change in the record panel history', async ({
  page,
}) => {
  await templateBase(page)
  const grid = gridOf(page)
  const row = rowWith(page, 'Steel utensils, 40 sets')
  await expect(row.getByRole('gridcell', { name: 'Sharma Traders' })).toBeVisible()

  // double-click a link cell to open the picker; the customer link is single, so a pick replaces
  await row.getByRole('gridcell', { name: 'Sharma Traders' }).dblclick()
  const picker = page.getByRole('dialog', { name: 'Link to Customers' })
  await picker.getByRole('combobox', { name: 'Search Customers' }).fill('Kavya')
  await picker.getByRole('option', { name: /Kavya Hegde/ }).click()
  await expect(picker.getByRole('button', { name: 'Remove Kavya Hegde' })).toBeVisible()
  await picker.getByRole('button', { name: 'Done' }).click()
  await expect(picker).toHaveCount(0)
  await expect(row.getByRole('gridcell', { name: 'Kavya Hegde' })).toBeVisible()

  // the record panel shows the new link, and its History tab lists the change
  await row.getByRole('button', { name: /^Open record/ }).click()
  const panel = page.getByRole('dialog', { name: /^Record / })
  await expect(panel).toBeVisible()
  await expect(panel.getByText('Kavya Hegde')).toBeVisible()
  await panel.getByRole('tab', { name: 'History' }).click()
  const history = panel.getByRole('list', { name: 'Change history' })
  await expect(history).toBeVisible()
  await expect(history.getByRole('listitem').first()).toContainText(/Customer/)
  await panel.getByRole('tab', { name: 'Details' }).click()
  await panel.getByRole('button', { name: 'Close record' }).click()
  await expect(panel).toHaveCount(0)
  await expect(grid).toBeVisible()
})
