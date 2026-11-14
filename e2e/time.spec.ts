import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

test('time: log on an item -> invoice unbilled hours -> entries show invoiced; milestone invoice', async ({
  page,
}) => {
  await signup(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  await page.goto(`${base}/crm`)
  const customer = `Client ${uniq()}`
  await page.getByRole('button', { name: 'New contact' }).click()
  const c = page.getByRole('dialog', { name: 'New contact' })
  await c.getByLabel('Name').fill(customer)
  await c.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('link', { name: customer })).toBeVisible()

  await page.getByRole('link', { name: 'Projects' }).click()
  await page.getByRole('button', { name: 'New project' }).click()
  const dlg = page.getByRole('dialog', { name: 'Create project' })
  await dlg.getByLabel('Name').fill(`Proj ${uniq()}`)
  const key = `T${uniq().slice(0, 4)}`.toUpperCase()
  await dlg.getByLabel('Key').fill(key)
  await dlg.getByRole('button', { name: 'Create project' }).click()
  await expect(page.getByRole('tab', { name: 'List' })).toBeVisible()
  await page.keyboard.press('c')
  const d = page.getByRole('dialog', { name: 'New item' })
  await d.getByLabel('Title').fill('Wireframes')
  await d.getByRole('button', { name: 'Create item' }).click()
  await expect(d).toBeHidden()
  await page.getByRole('link', { name: 'Wireframes' }).click()

  // log time on the item
  const card = page.getByRole('region', { name: 'Time' })
  await card.getByLabel('Time spent').fill('1h30')
  await expect(card.getByText('Will log 1h 30m')).toBeVisible()
  await card.getByRole('button', { name: 'Log time' }).click()
  await expect(card.getByText('1h 30m', { exact: true }).first()).toBeVisible()
  await expect(card.locator('span', { hasText: 'Billable' })).toBeVisible()
  await card.getByLabel('Time spent').fill('banana')
  await card.getByRole('button', { name: 'Log time' }).click()
  await expect(card.getByRole('alert')).toContainText('Enter the time')

  // project billing profile, then invoice the hours
  await page.goto(`${base}/projects/${key}`)
  await page.getByRole('tab', { name: 'Billing' }).click()
  await page.getByLabel('Customer', { exact: true }).selectOption({ label: customer })
  await page.getByLabel('Hourly rate (INR)').fill('1000')
  await page.getByLabel('SAC').fill('998314')
  await page.getByRole('button', { name: 'Save profile' }).click()
  await page.getByRole('tab', { name: 'Time' }).click()
  await expect(page.getByRole('status').filter({ hasText: '1h 30m billable' })).toContainText('₹1,500.00')
  await page.getByRole('button', { name: 'Create invoice' }).click()
  await page.getByRole('link', { name: 'Open it' }).click()
  await expect(page.getByRole('heading', { name: /Draft invoice/ })).toBeVisible()
  await expect(page.getByLabel('Line 1 description')).toHaveValue(/1 h 30 min/)

  await page.goto(`${base}/projects/${key}-1`)
  await expect(page.getByRole('region', { name: 'Time' }).getByText('Invoiced')).toBeVisible()

  // a fixed-amount milestone, invoiced on a chosen date
  await page.goto(`${base}/projects/${key}`)
  await page.getByRole('tab', { name: 'Billing' }).click()
  const f = page.getByRole('form', { name: 'Add milestone' })
  await f.getByLabel('Name').fill('Handover')
  await f.getByLabel('or fixed amount').fill('50000')
  await f.getByRole('button', { name: 'Add milestone' }).click()
  const row = page.getByRole('row', { name: /Handover/ })
  await expect(row.getByText('Planned')).toBeVisible()
  await row.getByLabel('Invoice date for Handover').fill('2026-03-31')
  await row.getByRole('button', { name: 'Raise invoice for Handover' }).click()
  await expect(page.getByRole('row', { name: /Handover/ }).getByText('Invoiced')).toBeVisible()
  await page
    .getByRole('row', { name: /Handover/ })
    .getByRole('link')
    .click()
  await expect(page.getByLabel('Line 1 description')).toHaveValue('Handover')
})
