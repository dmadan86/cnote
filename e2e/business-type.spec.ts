import { expect, test } from '@playwright/test'
import { signup } from './helpers'

const ALL = ['Tables', 'Docs', 'Projects', 'CRM', 'Billing', 'Reports', 'Whiteboard', 'Website']

test('pick Trading in setup, deal stages follow, every module stays, then change it in Settings', async ({
  page,
}) => {
  await signup(page, undefined, { wizard: true })
  await expect(page.getByRole('heading', { name: /What does your business do/ })).toBeFocused()
  await page.getByRole('radio', { name: /Trading company/ }).check({ force: true })
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(page.getByRole('heading', { name: /Your business details/ })).toBeFocused()
  await page.getByRole('button', { name: 'Skip for now' }).click()
  await expect(page).toHaveURL(/\/w\/[0-9a-f-]+$/)

  // a business type sets labels only: no module is hidden
  const nav = page.getByRole('navigation', { name: 'Modules' })
  for (const m of ALL) await expect(nav.getByRole('link', { name: m, exact: true })).toBeVisible()

  const base = new URL(page.url()).pathname
  await page.goto(`${base}/crm`)
  await page.getByRole('link', { name: 'Deals' }).click()
  await expect(page.getByRole('region', { name: 'Enquiry stage' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Ordered stage' })).toBeVisible()

  await page.goto(`${base}/settings`)
  await expect(page.getByRole('radio', { name: /Trading company/ })).toBeChecked()
  await page.getByRole('radio', { name: '51-200' }).click({ force: true })
  await expect(page.getByRole('radio', { name: '51-200' })).toBeChecked()
  const saved = page.waitForResponse((r) => r.url().includes('/commands/workspace.update'))
  await page.getByRole('radio', { name: /Project company/ }).click({ force: true })
  await saved
  await page.goto(`${base}/crm`)
  await page.getByRole('link', { name: 'Deals' }).click()
  await expect(page.getByRole('region', { name: 'Project awarded stage' })).toBeVisible()
  await page.goto(`${base}/settings`)
  await expect(page.getByRole('radio', { name: '51-200' })).toBeChecked()

  // the old "Set up your company" dialog is gone for good
  await page.reload()
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

test('no business type hides a module', async ({ page }) => {
  await signup(page)
  const base = new URL(page.url()).pathname
  await page.goto(`${base}/settings`)
  const nav = page.getByRole('navigation', { name: 'Modules' })
  for (const type of [
    /Service company/,
    /Project company/,
    /Trading company/,
    /Manufacturing/,
    /Recurring services/,
    /Professional firm/,
  ]) {
    await page.getByRole('radio', { name: type }).click({ force: true })
    await expect(page.getByRole('radio', { name: type })).toBeChecked()
    for (const m of ALL) await expect(nav.getByRole('link', { name: m, exact: true })).toBeVisible()
  }
})
