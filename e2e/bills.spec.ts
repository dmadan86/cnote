import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

const CHARS = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'
/** Valid 15-character GSTIN for a state and 10-character PAN-like body (mod-36 checksum). */
function gstin(state: string, pan: string) {
  const body = `${state}${pan}1Z`
  const sum = [...body].reduce((s, ch, i) => {
    const v = CHARS.indexOf(ch) * (i % 2 ? 2 : 1)
    return s + Math.floor(v / 36) + (v % 36)
  }, 0)
  return body + CHARS[(36 - (sum % 36)) % 36]
}

test('Purchase bill: vendor -> draft -> record -> partial and full payment', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  await page.goto(`${base}/billing/settings`)
  await page.getByLabel('GSTIN').fill(gstin('29', 'ABCDE1234F'))
  await page.getByLabel('State code').selectOption('29')
  await page.getByRole('button', { name: 'Save billing settings' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()

  await page.goto(`${base}/crm`)
  const vendor = `Vendor ${uniq()}`
  await page.getByRole('button', { name: 'New contact' }).click()
  const dialog = page.getByRole('dialog', { name: 'New contact' })
  await dialog.getByLabel('Name').fill(vendor)
  await dialog.getByLabel('GSTIN').fill(gstin('27', 'AAAAA0000A'))
  await dialog.getByLabel('This contact is a vendor').check()
  await dialog.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('link', { name: vendor })).toBeVisible()

  await page.goto(`${base}/billing/bills`)
  await expect(page.getByText('No bills yet.')).toBeVisible()
  await page.getByRole('button', { name: 'New bill' }).click()
  const nb = page.getByRole('dialog', { name: 'New bill' })
  await nb.getByLabel('Vendor', { exact: true }).selectOption({ label: vendor })
  const number = `SUP-${uniq()}`
  await nb.getByLabel('Vendor bill number').fill(number)
  await nb.getByRole('button', { name: 'Create draft' }).click()
  await expect(page).toHaveURL(/\/billing\/bills\/[0-9a-f-]+$/)

  await page.getByLabel('Line 1 description').fill('Cloud hosting')
  await page.getByLabel('Line 1 unit price').fill('10000')
  await page.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible()

  await page.getByRole('button', { name: 'Record bill' }).click()
  await page.getByRole('dialog', { name: 'Record bill' }).getByRole('button', { name: 'Record bill' }).click()
  await expect(page.getByText('Recorded', { exact: true })).toBeVisible()
  // Vendor in Maharashtra, received in Karnataka: inter-state, 18% IGST (input credit) on 10,000.
  const totals = page.getByLabel('Totals')
  await expect(totals.getByText('IGST')).toBeVisible()
  await expect(totals).toContainText('₹11,800.00')

  await page.getByRole('button', { name: 'Record payment' }).click()
  const pay = page.getByRole('dialog', { name: 'Record payment' })
  await pay.getByLabel('Amount (INR)').fill('5000')
  await pay.getByRole('button', { name: 'Record payment', exact: true }).click()
  await expect(page.getByText('Partly paid', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Record payment' }).click()
  await page
    .getByRole('dialog', { name: 'Record payment' })
    .getByRole('button', { name: 'Record payment', exact: true })
    .click()
  await expect(page.getByText('Paid', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Record payment' })).toHaveCount(0)

  await page.goto(`${base}/billing/bills`)
  await expect(page.getByRole('link', { name: number, exact: true })).toBeVisible()
})
