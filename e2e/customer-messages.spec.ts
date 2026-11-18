import { expect, type Page, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

const CHARS = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'
function gstin(state: string, pan: string) {
  const body = `${state}${pan}1Z`
  const sum = [...body].reduce((s, ch, i) => {
    const v = CHARS.indexOf(ch) * (i % 2 ? 2 : 1)
    return s + Math.floor(v / 36) + (v % 36)
  }, 0)
  return body + CHARS[(36 - (sum % 36)) % 36]
}

/** Owner side: workspace with GSTIN + UPI, one customer, space. Returns the workspace path and the customer's name. */
async function setup(page: Page) {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
  await page.goto(`${base}/billing/settings`)
  await page.getByLabel('GSTIN').fill(gstin('29', 'ABCDE1234F'))
  await page.getByLabel('State code').selectOption('29')
  await page.getByLabel('UPI ID (VPA)').fill('acme.shop@okbank')
  await page.getByLabel('UPI payee name').fill('Acme Traders')
  await page.getByLabel('Bank details shown on shared invoices').fill('HDFC Bank, A/c 0001, IFSC HDFC0000001')
  await page.getByRole('button', { name: 'Save billing settings' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()
  await page.goto(`${base}/crm`)
  const customer = `Buyer ${uniq()}`
  await page.getByRole('button', { name: 'New contact' }).click()
  const d = page.getByRole('dialog', { name: 'New contact' })
  await d.getByLabel('Name').fill(customer)
  await d.getByLabel('GSTIN').fill(gstin('29', 'AAAAA0000A'))
  await d.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('link', { name: customer })).toBeVisible()
  return { base, customer }
}

async function sentQuote(page: Page, base: string, customer: string) {
  await page.goto(`${base}/billing/quotes`)
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
}

test('admin edits a customer message, sees the preview, saves, resets; the Send dialog shows the wording', async ({
  page,
}) => {
  const { base, customer } = await setup(page)

  await page.goto(`${base}/settings?tab=Messages`)
  await expect(page.getByRole('heading', { name: 'Customer messages' })).toBeVisible()
  await expect(page.getByRole('form', { name: 'Look and sender' })).toBeVisible()
  await page.getByRole('button', { name: /^Invoice sent/ }).click()

  const subject = page.getByLabel('Subject')
  const mine = `Your invoice ${uniq()} is ready`
  await subject.fill(mine)
  await expect(page.getByText(`${mine}`, { exact: false }).first()).toBeVisible()
  await page.getByRole('button', { name: 'Customer name' }).click() // chip inserts at the cursor
  await expect(page.getByRole('textbox', { name: 'Message' })).toHaveValue(/\{\{contact\.name\}\}/)
  await expect(page.locator('iframe[title="Email preview"]')).toBeVisible()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()
  await expect(page.getByText('Customised')).toBeVisible()

  await page.getByRole('button', { name: 'Reset to default' }).click()
  const confirm = page.getByRole('dialog', { name: /Reset to default/ })
  await confirm.getByRole('button', { name: 'Reset to default' }).click()
  await expect(page.getByText('Customised')).toHaveCount(0)
  await expect(subject).not.toHaveValue(mine)

  // Send dialog: quotation wording prefilled from the server.
  await sentQuote(page, base, customer)
  await page.getByRole('button', { name: 'Share with customer' }).click()
  const dialog = page.getByRole('dialog', { name: 'Share quotation' })
  await dialog.getByRole('tab', { name: 'Email' }).click()
  await expect(dialog.getByLabel('Subject')).not.toHaveValue('')
  await expect(dialog.getByLabel('Message')).not.toHaveValue('')
  await dialog.getByRole('tab', { name: 'WhatsApp' }).click()
  await expect(dialog.getByRole('button', { name: 'Create link for WhatsApp' })).toBeVisible()
})
