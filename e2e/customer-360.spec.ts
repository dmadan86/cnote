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

test('Customer 360: deal -> quote -> accept -> invoice + project -> payment, all on the contact page', async ({
  page,
}) => {
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
  await page.getByRole('link', { name: customer }).click()
  await expect(page.getByRole('heading', { name: customer })).toBeVisible()
  const contactUrl = page.url()

  // Empty 360.
  await expect(page.getByRole('region', { name: 'Timeline' })).toContainText('Nothing has happened yet')

  // New deal, contact prefilled.
  await page.getByRole('button', { name: 'New deal' }).click()
  const deal = page.getByRole('dialog', { name: /New deal/ })
  await deal.getByLabel('Deal title').fill('Website inquiry')
  await expect(deal.getByLabel('Contact')).toHaveValue(/.+/)
  await deal.getByRole('button', { name: 'Create deal' }).click()
  await expect(page.getByRole('region', { name: 'Deals' })).toContainText('Website inquiry')

  // New quotation, contact prefilled; send, accept, convert to invoice and project.
  await page.getByRole('button', { name: 'New quotation' }).click()
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
  await page.getByRole('button', { name: 'Mark accepted' }).click()
  await expect(page.getByText('Accepted', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Convert to project' }).click()
  await page.getByRole('button', { name: 'Convert to invoice' }).click()
  await page.getByRole('link', { name: 'Open invoice draft' }).click()

  // Issue the invoice (18% IGST on 10,000 = 11,800) and receive 4,000.
  await page.getByRole('button', { name: 'Review & issue' }).click()
  await page
    .getByRole('dialog', { name: 'Review and issue' })
    .getByRole('button', { name: 'Issue invoice' })
    .click()
  await expect(page.getByRole('heading', { name: /^Invoice INV\// })).toBeVisible()
  await page.getByRole('button', { name: 'Record payment' }).click()
  const pay = page.getByRole('dialog', { name: 'Record payment' })
  await pay.getByLabel('Amount (INR)').fill('4000')
  await pay.getByRole('button', { name: 'Record payment' }).click()
  await expect(pay).toHaveCount(0)
  await expect(page.getByTestId('outstanding')).toHaveText('₹7,800.00')

  // Back on the contact: everything is there.
  await page.goto(contactUrl)
  const bal = page.getByRole('region', { name: 'Balance' })
  await expect(bal).toContainText('₹7,800.00')
  await expect(bal).toContainText('₹11,800.00')
  await expect(bal).toContainText('₹4,000.00')
  await expect(page.getByRole('region', { name: 'Quotations' })).toContainText(/QT\//)
  await expect(page.getByRole('region', { name: 'Projects' })).toContainText('Quotation for')
  await expect(page.getByRole('region', { name: 'Invoices' })).toContainText(/INV\//)
  await expect(page.getByRole('region', { name: 'Payments received' })).toContainText('₹4,000.00')
  const tl = page.getByRole('region', { name: 'Timeline' })
  for (const t of [
    'Inquiry "Website inquiry" created',
    /Quotation QT\/.* sent/,
    /Quotation QT\/.* accepted/,
    /Project .* started/,
    /Invoice INV\/.* issued/,
    /Payment ₹4,000.00 received/,
  ])
    await expect(tl).toContainText(t)
})
