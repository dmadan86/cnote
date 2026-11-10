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

test('GST invoice: draft -> IGST -> issue -> IRN -> partial and full payment -> nothing outstanding', async ({
  page,
}) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  // Seller in Karnataka (29).
  await page.goto(`${base}/billing/settings`)
  await page.getByLabel('GSTIN').fill(gstin('29', 'ABCDE1234F'))
  await page.getByLabel('State code').selectOption('29')
  await page.getByRole('button', { name: 'Save billing settings' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()

  // Bad GSTINs are rejected by the API and the message is shown.
  await page.goto(`${base}/crm`)
  const customer = `Buyer ${uniq()}`
  await page.getByRole('button', { name: 'New contact' }).click()
  const dialog = page.getByRole('dialog', { name: 'New contact' })
  await dialog.getByLabel('Name').fill(customer)
  await dialog.getByLabel('GSTIN').fill('27AAAAA0000A1Z0')
  await dialog.getByRole('button', { name: 'Save contact' }).click()
  await expect(dialog.getByRole('alert')).toContainText(/gstin/i)
  // Buyer in Maharashtra (27): inter-state, so IGST.
  await dialog.getByLabel('GSTIN').fill(gstin('27', 'AAAAA0000A'))
  await dialog.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('link', { name: customer })).toBeVisible()

  await page.goto(`${base}/billing`)
  await page.getByRole('button', { name: 'New invoice' }).click()
  await page
    .getByRole('dialog', { name: 'New invoice' })
    .getByLabel('Customer')
    .selectOption({ label: customer })
  await page.getByRole('button', { name: 'Create draft' }).click()
  await expect(page).toHaveURL(/\/billing\/[0-9a-f-]+$/)

  await page.getByLabel('Line 1 description').fill('Design work')
  await page.getByLabel('Line 1 unit price').fill('10000')
  await page.getByRole('button', { name: 'Add line' }).click()
  await page.getByLabel('Line 2 description').fill('Hosting')
  await page.getByLabel('Line 2 unit price').fill('5000')
  await page.getByLabel('Line 2 GST rate').selectOption('12')

  const totals = page.getByLabel('Totals')
  await expect(totals.getByText('IGST')).toBeVisible()
  await expect(totals.getByText('CGST')).toHaveCount(0)
  // 18% of 10,000 + 12% of 5,000 = 2,400 IGST on a 15,000 subtotal.
  await expect(totals).toContainText('₹17,400.00')
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible()

  await page.getByRole('button', { name: 'Review & issue' }).click()
  const review = page.getByRole('dialog', { name: 'Review and issue' })
  await expect(review.getByText('IGST')).toBeVisible()
  await expect(review).toContainText('₹2,400.00')
  await review.getByRole('button', { name: 'Issue invoice' }).click()
  await expect(page.getByRole('heading', { name: /^Invoice INV\// })).toBeVisible()

  await page.getByRole('button', { name: 'Generate IRN' }).click()
  await expect(page.getByTestId('irn')).toHaveText(/^[0-9a-f]{64}$/)
  await expect(page.getByText('IRN generated', { exact: true })).toBeVisible()

  const pay = async (amount: string | null) => {
    await page.getByRole('button', { name: 'Record payment' }).click()
    const d = page.getByRole('dialog', { name: 'Record payment' })
    if (amount) await d.getByLabel('Amount (INR)').fill(amount)
    await d.getByLabel('Method').selectOption('bank')
    await d.getByRole('button', { name: 'Record payment' }).click()
    await expect(d).toHaveCount(0)
  }
  await pay('10000')
  await expect(page.getByText('Partly paid', { exact: true })).toBeVisible()
  await expect(page.getByTestId('outstanding')).toHaveText('₹7,400.00')
  await pay(null) // prefilled with the outstanding balance
  await expect(page.getByText('Paid', { exact: true }).first()).toBeVisible()
  await expect(page.getByTestId('outstanding')).toHaveText('₹0.00')

  await page.goto(`${base}/billing/reports`)
  await expect(page.getByText('Nothing outstanding.')).toBeVisible()
  await expect(page.getByTestId('ageing-total')).toContainText('₹0.00')
})
