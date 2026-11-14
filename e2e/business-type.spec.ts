import { expect, test } from '@playwright/test'
import { signup } from './helpers'

test('pick Trading on first run, then change it in Settings', async ({ page }) => {
  await signup(page, undefined, { skipPicker: false })
  const picker = page.getByRole('dialog', { name: 'Set up your company' })
  // Step 1: type and size; country is shown but fixed.
  await expect(picker.getByText('Country: India')).toBeVisible()
  await expect(picker.getByRole('button', { name: 'Next' })).toBeDisabled()
  await picker.getByRole('radio', { name: /Trading company/ }).check({ force: true })
  await picker.getByRole('radio', { name: '11-50' }).check({ force: true })
  await picker.getByRole('button', { name: 'Next' }).click()
  // Step 2: modules pre-checked from the type's defaults; turn Billing off, Whiteboard on.
  await expect(picker.getByRole('checkbox', { name: /^CRM/ })).toBeChecked()
  await expect(picker.getByRole('checkbox', { name: /^Projects/ })).not.toBeChecked()
  await picker.getByRole('checkbox', { name: /^Whiteboard/ }).check()
  await picker.getByRole('checkbox', { name: /^Billing/ }).uncheck()
  await picker.getByRole('button', { name: 'Continue' }).click()
  await expect(picker).toHaveCount(0)

  const nav = page.getByRole('navigation', { name: 'Modules' })
  await expect(nav.getByRole('link', { name: 'CRM' })).toBeVisible()
  await expect(nav.getByRole('link', { name: 'Whiteboard' })).toBeVisible()
  await expect(nav.getByRole('link', { name: 'Billing' })).toHaveCount(0)
  await expect(nav.getByRole('link', { name: 'Projects' })).toHaveCount(0)

  const base = new URL(page.url()).pathname
  await page.goto(`${base}/crm`)
  await page.getByRole('link', { name: 'Deals' }).click()
  await expect(page.getByRole('region', { name: 'Enquiry stage' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Ordered stage' })).toBeVisible()

  await page.goto(`${base}/settings`)
  await expect(page.getByRole('radio', { name: /Trading company/ })).toBeChecked()
  await expect(page.getByRole('radio', { name: '11-50' })).toBeChecked()
  await page.getByRole('radio', { name: '51-200' }).click({ force: true })
  await expect(page.getByRole('radio', { name: '51-200' })).toBeChecked()
  await page.getByRole('radio', { name: /Project company/ }).click({ force: true })
  await expect(nav.getByRole('link', { name: 'Projects' })).toBeVisible()
  await page.goto(`${base}/crm`)
  await page.getByRole('link', { name: 'Deals' }).click()
  await expect(page.getByRole('region', { name: 'Project awarded stage' })).toBeVisible()
  await page.goto(`${base}/settings`)
  await expect(page.getByRole('radio', { name: '51-200' })).toBeChecked()

  // Reload: the chooser does not return once a type is set.
  await page.reload()
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

test('Skip on step 2 closes the chooser and does not return', async ({ page }) => {
  await signup(page, undefined, { skipPicker: false })
  const picker = page.getByRole('dialog', { name: 'Set up your company' })
  await picker.getByRole('radio', { name: /Service company/ }).check({ force: true })
  await picker.getByRole('button', { name: 'Next' }).click()
  await picker.getByRole('button', { name: 'Skip' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.reload()
  await expect(page.getByRole('dialog')).toHaveCount(0)
})
