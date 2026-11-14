import { expect, test } from '@playwright/test'
import { signup } from './helpers'

test('pick Trading on first run, then change it in Settings', async ({ page }) => {
  await signup(page, undefined, { skipPicker: false })
  const picker = page.getByRole('dialog', { name: 'What kind of business do you run?' })
  await picker.getByRole('radio', { name: /Trading company/ }).check({ force: true })
  await expect(picker).toHaveCount(0)

  const nav = page.getByRole('navigation', { name: 'Modules' })
  await expect(nav.getByRole('link', { name: 'CRM' })).toBeVisible()
  await expect(nav.getByRole('link', { name: 'Billing' })).toBeVisible()
  await expect(nav.getByRole('link', { name: 'Projects' })).toHaveCount(0)

  const base = new URL(page.url()).pathname
  await page.goto(`${base}/crm`)
  await page.getByRole('link', { name: 'Deals' }).click()
  await expect(page.getByRole('region', { name: 'Enquiry stage' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Ordered stage' })).toBeVisible()

  await page.goto(`${base}/settings`)
  await expect(page.getByRole('radio', { name: /Trading company/ })).toBeChecked()
  await page.getByRole('radio', { name: /Project company/ }).check({ force: true })
  await expect(nav.getByRole('link', { name: 'Projects' })).toBeVisible()
  await page.goto(`${base}/crm`)
  await page.getByRole('link', { name: 'Deals' }).click()
  await expect(page.getByRole('region', { name: 'Project awarded stage' })).toBeVisible()

  // Reload: the chooser does not return once a type is set.
  await page.reload()
  await expect(page.getByRole('dialog')).toHaveCount(0)
})
