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

test('Advance: accept quote -> record advance -> convert -> issue -> apply -> balance due', async ({
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
  await expect(page.getByRole('link', { name: customer })).toBeVisible()

  await page.goto(`${base}/billing/quotes`)
  await page.getByRole('button', { name: 'New quote' }).click()
  await page
    .getByRole('dialog', { name: 'New quote' })
    .getByLabel('Customer')
    .selectOption({ label: customer })
  await page.getByRole('button', { name: 'Create draft' }).click()
  await page.getByLabel('Line 1 description').fill('Website design')
  await page.getByLabel('Line 1 unit price').fill('10000')
  await page.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible()
  await page.getByRole('button', { name: 'Send quote' }).click()
  await page
    .getByRole('dialog', { name: 'Send quotation' })
    .getByRole('button', { name: 'Send quote' })
    .click()
  await page.getByRole('button', { name: 'Mark accepted' }).click()
  await expect(page.getByText('Accepted', { exact: true })).toBeVisible()

  // 30% advance of Rs 11,800 = Rs 3,540 gross, GST 18% included; buyer in Maharashtra: IGST
  await page.getByRole('button', { name: 'Record advance' }).click()
  const adv = page.getByRole('dialog', { name: 'Record advance' })
  await adv.getByLabel('Amount received (INR, including GST)').fill('3540')
  await expect(adv.getByRole('status')).toContainText('Taxable value ₹3,000.00 + GST ₹540.00')
  await adv.getByLabel('Reference (UTR, cheque no.)').fill('UTR12345')
  await adv.getByRole('button', { name: 'Issue receipt voucher' }).click()
  await expect(page).toHaveURL(/\/billing\/receipts\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: /^Receipt voucher RV\// })).toBeVisible()
  await expect(page.getByTestId('receipt-amount')).toHaveText('₹3,540.00')
  await expect(page.getByText('IGST 18%')).toBeVisible()
  await expect(page.getByTestId('unapplied')).toContainText('₹3,540.00')

  // the customer page shows the unapplied balance
  await page.goto(`${base}/crm`)
  await page.getByRole('table').getByRole('link', { name: customer }).click()
  await expect(page.getByTestId('advance-balance')).toHaveText('Unapplied ₹3,540.00')

  // convert, issue, apply
  await page.goto(`${base}/billing/quotes`)
  await page.getByRole('link', { name: /^QT\// }).click()
  await page.getByRole('button', { name: 'Convert to invoice' }).click()
  await page.getByRole('link', { name: 'Open invoice draft' }).click()
  await page.getByRole('button', { name: 'Review & issue' }).click()
  const review = page.getByRole('dialog', { name: 'Review and issue' })
  await review.getByRole('button', { name: 'Issue invoice' }).click()
  await expect(page.getByRole('heading', { name: /^Invoice INV\// })).toBeVisible()
  await expect(page.getByTestId('outstanding')).toHaveText('₹11,800.00')

  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  const apply = page.getByRole('dialog', { name: /^Apply advance RV\// })
  await expect(apply.getByLabel('Amount to apply (INR)')).toHaveValue('3540')
  await apply.getByRole('button', { name: 'Apply advance' }).click()
  await expect(page.getByTestId('outstanding')).toHaveText('₹8,260.00')
  await expect(page.getByText('advance', { exact: false }).first()).toBeVisible()

  // the voucher now shows where it went
  await page.goto(`${base}/crm`)
  await page.getByRole('table').getByRole('link', { name: customer }).click()
  await expect(page.getByTestId('advance-balance')).toHaveText('Unapplied ₹0.00')
})
