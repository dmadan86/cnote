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

test('Trader keeps stock: item, opening stock, bill receives, invoice issues, low-stock tile, reversal', async ({
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
  const sku = `SKU-${uniq()}`

  // item and opening stock through the UI
  await page.goto(`/w/${ws}/billing/stock`)
  await expect(page.getByText('No items yet')).toBeVisible()
  await page.getByRole('button', { name: 'New item' }).click()
  const form = page.getByRole('dialog', { name: 'New item' })
  await form.getByLabel('SKU').fill(sku)
  await form.getByLabel('Name', { exact: true }).fill('Steel bolt')
  await form.getByLabel('HSN code (4, 6 or 8 digits)').fill('7318')
  await form.getByLabel('Selling price per unit (INR)').fill('250')
  await form.getByLabel('Reorder level (0 = no alert)').fill('8')
  await form.getByRole('button', { name: 'Save' }).click()
  const row = page.getByRole('row', { name: /Steel bolt/ })
  await expect(row).toContainText('0 pcs')

  await row.getByRole('button', { name: 'Adjust' }).click()
  const adj = page.getByRole('dialog', { name: /Adjust stock: Steel bolt/ })
  await adj.getByLabel('Quantity (pcs)').fill('10')
  await adj.getByLabel('Reason').selectOption('Opening stock')
  await adj.getByLabel(/Cost per unit/).fill('100')
  await adj.getByRole('button', { name: 'Save adjustment' }).click()
  await expect(row).toContainText('10 pcs')
  await expect(page.getByText('Stock value, weighted average cost')).toBeVisible()

  // a recorded bill receives stock, an issued invoice takes it out
  const item = (await call('stock_item.list', {}))[0].id as string
  const vendor = (
    await call('contact.create', {
      space_id: space,
      name: 'Bolt Supplier',
      kind: 'company',
      gstin: gstin('29', 'AABCT1332L'),
      is_vendor: true,
    })
  ).id
  const bill = (
    await call('bill.create_draft', {
      space_id: space,
      vendor_id: vendor,
      vendor_bill_number: `B-${uniq()}`,
      bill_date: new Date().toISOString().slice(0, 10),
      lines: [{ description: 'Bolts', quantity: 5, unit_price_minor: 20_000, stock_item_id: item }],
    })
  ).id
  await call('bill.record', { id: bill })
  const buyer = (
    await call('contact.create', {
      space_id: space,
      name: 'Buyer',
      kind: 'company',
      gstin: gstin('29', 'AABCT1332L'),
    })
  ).id
  const inv = (
    await call('invoice.create_draft', {
      space_id: space,
      contact_id: buyer,
      lines: [{ description: 'Bolts', quantity: 12, unit_price_minor: 25_000, stock_item_id: item }],
    })
  ).id
  await call('invoice.issue', { id: inv })

  await page.goto(`/w/${ws}/billing/stock`)
  await expect(page.getByRole('row', { name: /Steel bolt/ })).toContainText('3 pcs')
  await expect(page.getByRole('row', { name: /Steel bolt/ })).toContainText('Low stock')
  await page.goto(`/w/${ws}`)
  await expect(page.getByRole('link', { name: /Low stock/ })).toContainText('1')

  // the ledger on the item page lists every posting; cancelling the invoice appends a reversal
  await call('invoice.cancel', { id: inv, reason: 'wrong buyer' })
  await page.goto(`/w/${ws}/billing/stock`)
  await page.getByRole('link', { name: 'Steel bolt' }).click()
  await expect(page.getByRole('row', { name: /Reversal \(cancelled\)/ })).toContainText('+12 pcs')
  await expect(page.getByRole('row', { name: /Received \(bill\)/ })).toContainText('+5 pcs')
  await expect(page.getByText('15 pcs').first()).toBeVisible()

  // a line editor offers the item
  const draft = (
    await call('invoice.create_draft', {
      space_id: space,
      contact_id: buyer,
      lines: [{ description: 'x', unit_price_minor: 1 }],
    })
  ).id
  await page.goto(`/w/${ws}/billing/${draft}`)
  await page.getByLabel('Line 1 stock item').selectOption(item)
  await expect(page.getByLabel('Line 1 description')).toHaveValue('Steel bolt')
  await expect(page.getByLabel('Line 1 unit price')).toHaveValue('250')
})
