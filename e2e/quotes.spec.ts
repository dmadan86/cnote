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

test('Quotation: draft -> send -> accept -> convert to invoice draft', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  await page.goto(`${base}/billing/settings`)
  await page.getByLabel('GSTIN').fill(gstin('29', 'ABCDE1234F'))
  await page.getByLabel('State code').selectOption('29')
  await page.getByRole('button', { name: 'Save billing settings' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()

  await page.goto(`${base}/crm`)
  const customer = `Buyer ${uniq()}`
  await page.getByRole('button', { name: 'New contact' }).click()
  const dialog = page.getByRole('dialog', { name: 'New contact' })
  await dialog.getByLabel('Name').fill(customer)
  await dialog.getByLabel('GSTIN').fill(gstin('27', 'AAAAA0000A'))
  await dialog.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('link', { name: customer })).toBeVisible()

  await page.goto(`${base}/billing/quotes`)
  await expect(page.getByText('No quotes yet.')).toBeVisible()
  await page.getByRole('button', { name: 'New quote' }).click()
  await page
    .getByRole('dialog', { name: 'New quote' })
    .getByLabel('Customer')
    .selectOption({ label: customer })
  await page.getByRole('button', { name: 'Create draft' }).click()
  await expect(page).toHaveURL(/\/billing\/quotes\/[0-9a-f-]+$/)

  await page.getByLabel('Line 1 description').fill('Website design')
  await page.getByLabel('Line 1 unit price').fill('10000')
  await page.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible()

  await page.getByRole('button', { name: 'Send quote' }).click()
  await page
    .getByRole('dialog', { name: 'Send quotation' })
    .getByRole('button', { name: 'Send quote' })
    .click()
  await expect(page.getByRole('heading', { name: /^Quotation QT\// })).toBeVisible()
  // Buyer in Maharashtra: inter-state, 18% IGST on 10,000.
  const totals = page.getByLabel('Totals')
  await expect(totals.getByText('IGST')).toBeVisible()
  await expect(totals).toContainText('₹11,800.00')

  await page.getByRole('button', { name: 'Mark accepted' }).click()
  await expect(page.getByText('Accepted', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Convert to invoice' }).click()
  await expect(page.getByText('Converted', { exact: true })).toBeVisible()

  await page.getByRole('link', { name: 'Open invoice draft' }).click()
  await expect(page).toHaveURL(/\/billing\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: /Draft invoice/ })).toBeVisible()
  await expect(page.getByLabel('Line 1 description')).toHaveValue('Website design')

  await page.goto(`${base}/billing`)
  await expect(page.getByRole('link', { name: 'Draft' })).toBeVisible()
  await page.goto(`${base}/billing/quotes`)
  await expect(page.getByRole('link', { name: /^QT\// })).toBeVisible()
})
