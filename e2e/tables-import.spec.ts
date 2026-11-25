import { expect, test } from '@playwright/test'
import { gridOf, templateBase } from './tables-helpers'

test('CSV import: upload, match columns, review, and 30 more records appear', async ({ page }) => {
  await templateBase(page)
  await expect(page.getByText('10 records')).toBeVisible()
  await page.getByRole('button', { name: 'Import CSV' }).click()
  const dlg = page.getByRole('dialog', { name: 'Import a CSV into Orders' })
  await dlg.locator('input[type=file]').setInputFiles('e2e/fixtures/tables/orders.csv')

  // columns that match a field are matched; the ones that cannot be imported into a computed or link field are skipped
  await expect(dlg.getByLabel('Where Item goes')).toBeVisible()
  await dlg.getByLabel('Where Order no goes').selectOption('skip')
  await dlg.getByLabel('Where Customer goes').selectOption('skip')
  await dlg.getByRole('button', { name: 'Review' }).click()
  await expect(dlg.getByText(/records are ready/)).toBeVisible()
  await expect(dlg.getByText('30', { exact: true }).first()).toBeVisible()
  await dlg.getByRole('button', { name: 'Import 30 records' }).click()
  await expect(dlg.getByText('Imported 30 records.')).toBeVisible({ timeout: 30_000 })
  await dlg.getByRole('button', { name: 'Done' }).click()

  await expect(page.getByText(/^40 records/)).toBeVisible()
  await expect(gridOf(page)).toBeVisible()
})
