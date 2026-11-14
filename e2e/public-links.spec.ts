import { type Browser, expect, type Page, test } from '@playwright/test'
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

/** Opens the Share dialog, creates a link and returns its URL. */
async function createLink(page: Page) {
  await page.getByRole('button', { name: /Share with customer|Remind or share/ }).click()
  const dialog = page.getByRole('dialog', { name: /^Share / })
  await dialog.getByRole('button', { name: /Create (another )?link/ }).click()
  const url = await dialog.getByLabel('Share link').inputValue()
  expect(url).toMatch(/\/p\/[A-Za-z0-9_-]{43}$/)
  return { dialog, url }
}

/** A logged-out phone-sized visitor. */
async function visitor(browser: Browser) {
  const ctx = await browser.newContext({
    viewport: { width: 360, height: 740 },
    storageState: { cookies: [], origins: [] },
  })
  return { ctx, page: await ctx.newPage() }
}

test('customer opens a quote link on a phone, accepts it; owner sees views and the decision', async ({
  page,
  browser,
}) => {
  const { base, customer } = await setup(page)
  await sentQuote(page, base, customer)
  const { dialog, url } = await createLink(page)
  await expect(dialog.getByRole('link', { name: 'Send on WhatsApp' })).toHaveAttribute(
    'href',
    /^https:\/\/wa\.me\/\?text=/,
  )
  await dialog.getByRole('button', { name: 'Close' }).click()

  const v = await visitor(browser)
  await v.page.goto(url)
  await expect(v.page.getByRole('heading', { name: /^Quotation QT\// })).toBeVisible()
  await expect(v.page.getByTestId('doc-total')).toHaveText('₹11,800.00')
  await expect(v.page.getByTestId('doc-status')).toHaveText('Awaiting your response')
  expect(await v.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true) // no sideways scroll
  await expect(v.page.getByRole('list', { name: 'Items' }).getByText('Website design')).toBeVisible()

  // Two steps: the first click only opens the review; nothing is accepted until Confirm.
  await v.page.getByRole('button', { name: 'Accept quotation' }).click()
  const confirm = v.page.getByRole('button', { name: 'Confirm acceptance' })
  await expect(confirm).toBeDisabled() // a name is required
  await v.page.getByLabel('Your name').fill('Ravi Kumar')
  await v.page.getByLabel('Note for the seller (optional)').fill('Please start Monday')
  await confirm.click()
  await expect(v.page.getByTestId('decision')).toContainText('Accepted by Ravi Kumar')
  await expect(v.page.getByRole('button', { name: 'Accept quotation' })).toHaveCount(0)
  await v.page.reload() // the decision survives a reload
  await expect(v.page.getByTestId('decision')).toContainText('Accepted by Ravi Kumar')
  await v.ctx.close()

  await page.reload()
  await expect(page.getByTestId('decided-by')).toContainText('Accepted by Ravi Kumar on the shared link')
  await expect(page.getByRole('button', { name: 'Convert to invoice' })).toBeVisible()
  await page.getByRole('button', { name: 'Share with customer' }).click()
  const links = page.getByRole('dialog', { name: 'Share quotation' }).getByLabel('Links')
  await expect(links).toContainText('active')
  await expect(links).toContainText(/[2-9] views/) // open, reload, and the accept response's reload
})

test('a revoked link stops working', async ({ page, browser }) => {
  const { base, customer } = await setup(page)
  await sentQuote(page, base, customer)
  const { dialog, url } = await createLink(page)
  const v = await visitor(browser)
  await v.page.goto(url)
  await expect(v.page.getByRole('heading', { name: /^Quotation QT\// })).toBeVisible()
  await dialog.getByRole('button', { name: /^Revoke link/ }).click()
  await expect(dialog.getByLabel('Links')).toContainText('revoked')
  await v.page.goto(url)
  await expect(v.page.getByRole('heading', { name: 'Link unavailable' })).toBeVisible()
  await expect(v.page.getByText('Website design')).toHaveCount(0)
  await v.page.goto(url.replace(/.{5}$/, 'AAAAA')) // a made-up link looks the same
  await expect(v.page.getByRole('heading', { name: 'Link unavailable' })).toBeVisible()
  await v.ctx.close()
})

test('invoice link shows the amount, UPI intent and QR, bank details and a print button', async ({
  page,
  browser,
}) => {
  const { base, customer } = await setup(page)
  const ws = base.split('/')[2]
  const call = async (name: string, body: unknown) => {
    const r = await page.context().request.post(`/api/w/${ws}/commands/${name}`, { data: body })
    expect(r.ok(), await r.text()).toBe(true)
    return r.json()
  }
  const contacts = await call('contact.list', {})
  const spaces = await call('space.list', {})
  const draft = await call('invoice.create_draft', {
    space_id: spaces[0].id,
    contact_id: contacts.find((c: { name: string }) => c.name === customer).id,
    lines: [{ description: 'Hosting', unit_price_minor: 100_000, gst_rate_bp: 1800 }],
  })
  await call('invoice.issue', { id: draft.id })
  await page.goto(`${base}/billing/${draft.id}`)
  await expect(page.getByLabel('UPI payment').getByRole('img', { name: 'UPI payment QR' })).toBeVisible()
  const { dialog, url } = await createLink(page)
  await dialog.getByRole('button', { name: 'Close' }).click()

  const v = await visitor(browser)
  await v.page.goto(url)
  await expect(v.page.getByTestId('amount-due')).toHaveText('₹1,180.00')
  const pay = v.page.getByRole('link', { name: 'Pay by UPI' })
  await expect(pay).toHaveAttribute(
    'href',
    /^upi:\/\/pay\?pa=acme\.shop%40okbank&pn=Acme%20Traders&am=1180\.00&cu=INR&tn=INV%2F/,
  )
  await expect(v.page.getByRole('img', { name: 'UPI payment QR code' })).toBeVisible()
  await expect(v.page.getByText('HDFC Bank, A/c 0001')).toBeVisible()
  await expect(v.page.getByRole('button', { name: 'Print or save as PDF' })).toBeVisible()
  await expect(v.page.getByRole('button', { name: /Accept/ })).toHaveCount(0)
  expect(await v.page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  // print: only the document remains
  await v.page.emulateMedia({ media: 'print' })
  await expect(v.page.getByRole('button', { name: 'Print or save as PDF' })).toBeHidden()
  await expect(pay).toBeHidden()
  await expect(v.page.getByText('Hosting').first()).toBeVisible()
  await v.ctx.close()
})
