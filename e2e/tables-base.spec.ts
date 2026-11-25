import { expect, test } from '@playwright/test'
import { gridOf, templateBase, wsOf } from './tables-helpers'

test('a base made from the Order tracker template shows its sample records', async ({ page }) => {
  await templateBase(page, 'Order tracker', 'Orders 2026')
  // the sidebar entry leads back to the list, and the new base is there
  await page.getByRole('link', { name: 'Tables', exact: true }).first().click()
  await expect(page).toHaveURL(new RegExp(`${wsOf(page)}/tables$`))
  await page.getByRole('link', { name: /Orders 2026/ }).click()

  await expect(page.getByRole('tab', { name: 'Orders', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(page.getByRole('tab', { name: 'Customers', exact: true })).toBeVisible()
  const grid = gridOf(page)
  await expect(grid).toBeVisible()
  await expect(page.getByText('10 records')).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'Steel utensils, 40 sets' })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'Sharma Traders' }).first()).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Kanban: By status' })).toBeVisible()

  await page.getByRole('tab', { name: 'Customers', exact: true }).click()
  await expect(page.getByText('7 records')).toBeVisible()
  await expect(gridOf(page, 'Customers').getByRole('gridcell', { name: 'Kavya Hegde' })).toBeVisible()
})
