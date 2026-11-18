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

test('admin configures the payment reminder schedule; an issued invoice shows Reminders with Pause; a contact can opt out', async ({
  page,
}) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string

  // Schedule: toggle a step, change its days, save, reload keeps it.
  await page.goto(`${base}/settings?tab=Messages`)
  const form = page.getByRole('form', { name: 'Payment reminders' })
  await expect(form).toContainText('Emailed at 10:00 am IST')
  const step1 = form.getByRole('switch', { name: 'Step 1 on' })
  const was = await step1.isChecked()
  await step1.setChecked(!was)
  await form.getByLabel('Step 4 days').fill('9')
  await form.getByRole('button', { name: 'Save reminders' }).click()
  await expect(form.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()
  await page.reload()
  const after = page.getByRole('form', { name: 'Payment reminders' })
  await expect(after.getByRole('switch', { name: 'Step 1 on' })).toBeChecked({ checked: !was })
  await expect(after.getByLabel('Step 4 days')).toHaveValue('9')

  // Contact opt-out persists.
  await page.goto(`${base}/crm`)
  const customer = `Buyer ${uniq()}`
  await page.getByRole('button', { name: 'New contact' }).click()
  const d = page.getByRole('dialog', { name: 'New contact' })
  await d.getByLabel('Name').fill(customer)
  await d.getByRole('button', { name: 'Save contact' }).click()
  await page.getByRole('link', { name: customer }).click()
  const optOut = page.getByRole('switch', { name: "Don't send payment reminders" })
  await optOut.click() // saved on the server, then redrawn
  await expect(optOut).toBeChecked()
  await page.reload()
  await expect(page.getByRole('switch', { name: "Don't send payment reminders" })).toBeChecked()

  // Invoice view: Reminders section with Pause toggle. Issuing needs the seller's GSTIN and state.
  await page.goto(`${base}/billing/settings`)
  await page.getByLabel('GSTIN').fill(gstin('29', 'ABCDE1234F'))
  await page.getByLabel('State code').selectOption('29')
  await page.getByRole('button', { name: 'Save billing settings' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()
  await page.goto(`${base}/billing`)
  await page.getByRole('button', { name: 'New invoice' }).click()
  await page
    .getByRole('dialog', { name: 'New invoice' })
    .getByLabel('Customer')
    .selectOption({ label: customer })
  await page.getByRole('button', { name: 'Create draft' }).click()
  await expect(page).toHaveURL(/\/billing\/[0-9a-f-]+$/)
  await page.getByLabel('Line 1 description').fill('Design work')
  await page.getByLabel('Line 1 unit price').fill('1000')
  await page.getByLabel('Place of supply').selectOption('29')
  await expect(page.getByRole('status').filter({ hasText: 'Draft saved' })).toBeVisible()
  await page.getByRole('button', { name: 'Review & issue' }).click()
  await page
    .getByRole('dialog', { name: 'Review and issue' })
    .getByRole('button', { name: 'Issue invoice' })
    .click()
  await expect(page.getByRole('heading', { name: /^Invoice INV\// })).toBeVisible()
  const reminders = page.getByRole('region', { name: 'Reminders' })
  await expect(reminders).toBeVisible()
  const pause = reminders.getByRole('switch', { name: 'Pause reminders' })
  await pause.click()
  await expect(reminders).toContainText('paused')
  await pause.click()
  await expect(reminders).not.toContainText('paused')
})
