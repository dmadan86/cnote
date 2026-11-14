import { expect, test } from '@playwright/test'
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
const day = (n: number) => new Date(Date.now() + n * 86_400_000 + 330 * 60_000).toISOString().slice(0, 10) // the server counts days in IST

test('Batch item: receive two batches on a bill, invoice takes the earliest expiry, expiry report and tile', async ({
  page,
}) => {
  await signup(page)
  await createSpace(page)
  const ws = new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
  const call = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
    return r.json()
  }
  await call('workspace.update', { settings: { gstin: gstin('29', 'ABCDE1234F'), state_code: '29' } })
  const spaces = await call('space.list', {})
  const space = spaces[spaces.length - 1].id as string

  // a batch-tracked item through the UI
  await page.goto(`/w/${ws}/billing/stock`)
  await page.getByRole('button', { name: 'New item' }).click()
  const form = page.getByRole('dialog', { name: 'New item' })
  await form.getByLabel('SKU').fill(`MED-${uniq()}`)
  await form.getByLabel('Name', { exact: true }).fill('Cough syrup')
  await form.getByLabel('Selling price per unit (INR)').fill('120')
  await form.getByLabel('Tracking').selectOption('batch')
  await form.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('row', { name: /Cough syrup/ })).toContainText('Batches')
  const item = (await call('stock_item.list', {}))[0].id as string

  // receive: two bill lines for two batches, entered in the bill editor
  const vendor = (
    await call('contact.create', {
      space_id: space,
      name: 'Pharma Supplier',
      kind: 'company',
      gstin: gstin('29', 'AABCT1332L'),
      is_vendor: true,
    })
  ).id
  const line = { description: 'Syrup', quantity: 6, unit_price_minor: 5_000, stock_item_id: item }
  const bill = (
    await call('bill.create_draft', {
      space_id: space,
      vendor_id: vendor,
      vendor_bill_number: `B-${uniq()}`,
      bill_date: day(0),
      lines: [line, { ...line, quantity: 4 }],
    })
  ).id
  await page.goto(`/w/${ws}/billing/bills/${bill}`)
  await page.getByLabel('Line 1 batch number').fill('LATE-1')
  await page.getByLabel('Line 1 expiry date').fill(day(120))
  await page.getByLabel('Line 2 batch number').fill('SOON-1')
  await page.getByLabel('Line 2 expiry date').fill(day(15))
  await page.getByRole('button', { name: 'Record bill' }).click()
  await page.getByRole('dialog', { name: 'Record bill' }).getByRole('button', { name: 'Record bill' }).click()
  await expect(page.getByRole('dialog')).toBeHidden()

  await page.goto(`/w/${ws}/billing/stock/${item}`)
  await expect(page.getByRole('row', { name: /^SOON-1/ })).toContainText('4 pcs')
  await expect(page.getByRole('row', { name: /^SOON-1/ })).toContainText('Expires in 15 d')
  await expect(page.getByRole('row', { name: /^LATE-1/ })).toContainText('6 pcs')

  // invoice 7 units in the editor: it explains first-expiry-first-out, issuing takes SOON-1 (4) then LATE-1 (3)
  const buyer = (
    await call('contact.create', {
      space_id: space,
      name: 'Chemist',
      kind: 'company',
      gstin: gstin('29', 'AABCT1332L'),
    })
  ).id
  const inv = (
    await call('invoice.create_draft', {
      space_id: space,
      contact_id: buyer,
      lines: [{ description: 'x', unit_price_minor: 1 }],
    })
  ).id
  await page.goto(`/w/${ws}/billing/${inv}`)
  await page.getByLabel('Line 1 stock item').selectOption(item)
  await page.getByLabel('Line 1 quantity').fill('7')
  await expect(page.getByText('first-expiry-first-out')).toBeVisible()
  await expect(page.getByLabel('Line 1 available batches')).toContainText('SOON-1')
  await page.getByRole('button', { name: /Review/ }).click()
  await page
    .getByRole('dialog', { name: 'Review and issue' })
    .getByRole('button', { name: 'Issue invoice' })
    .click()
  await expect(page.getByRole('dialog')).toBeHidden()

  await page.goto(`/w/${ws}/billing/stock/${item}`)
  await expect(page.getByRole('row', { name: /^LATE-1/ })).toContainText('3 pcs')
  await expect(page.getByRole('row', { name: /^SOON-1/ })).toHaveCount(0) // sold out: no longer listed

  // the expiry report and home tile
  await call('stock.adjust', {
    item_id: item,
    qty: 2,
    reason: 'found',
    unit_cost_minor: 5_000,
    tracking: { batch: 'NEAR-1', expiry_date: day(10) },
  })
  await page.goto(`/w/${ws}/billing/stock/expiry`)
  await expect(page.getByRole('row', { name: /NEAR-1/ })).toContainText('Expires in 10 d')
  await page.goto(`/w/${ws}`)
  await expect(page.getByRole('link', { name: /Expiring in 30 days/ })).toContainText('1')
})
