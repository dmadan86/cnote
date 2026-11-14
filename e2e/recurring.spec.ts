import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('Recurring invoice: create, pause, resume, run now and open draft', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]
  const customer = `Recurring buyer ${uniq()}`
  await page.goto(`${base}/crm`)
  await page.getByRole('button', { name: 'New contact' }).click()
  const contact = page.getByRole('dialog', { name: 'New contact' })
  await contact.getByLabel('Name').fill(customer)
  await contact.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('link', { name: customer })).toBeVisible()
  await page.goto(`${base}/billing`)
  await page.getByRole('link', { name: 'Recurring', exact: true }).click()
  await page.getByRole('button', { name: 'New recurring invoice' }).click()
  const dialog = page.getByRole('dialog', { name: 'New recurring invoice' })
  await dialog.getByLabel('Customer').selectOption({ label: customer })
  const today = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Kolkata',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date())
  await dialog.getByLabel('Start date').fill(today)
  await dialog.getByLabel('Line 1 description').fill('Monthly retainer')
  await dialog.getByLabel('Line 1 price').fill('1000')
  await dialog.getByRole('button', { name: 'Create schedule' }).click()
  await expect(dialog).not.toBeVisible()
  await expect(page.getByText('active', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Pause', exact: true }).click()
  await expect(page.getByText('paused', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Resume', exact: true }).click()
  await page.getByRole('button', { name: 'Run now', exact: true }).click()
  await page.getByRole('link', { name: 'Open latest invoice' }).click()
  await expect(page.getByRole('heading', { name: /Draft invoice/ })).toBeVisible()
  await expect(page.getByLabel('Line 1 description')).toHaveValue('Monthly retainer')
  await expect(page.getByLabel('Line 1 unit price')).toHaveValue('1000')
  await page.goto(`${base}/billing/recurring`)
  page.once('dialog', (d) => d.accept())
  await page.getByRole('button', { name: 'End', exact: true }).click()
  await expect(page.getByText('ended', { exact: true })).toBeVisible()
})
