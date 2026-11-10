import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

test('CSV import of 3 rows completes and items appear', async ({ page }) => {
  await signup(page)
  const ws = page.url().match(/\/w\/([0-9a-f-]+)/)?.[1]
  await page.getByRole('button', { name: 'New space' }).click()
  const d = page.getByRole('dialog', { name: 'Create space' })
  await d.getByLabel('Name').fill(`Proj ${uniq()}`)
  await d.getByLabel('Key').fill(`P${uniq().slice(0, 4)}`)
  await d.getByLabel('Template').selectOption('software')
  await d.getByRole('button', { name: 'Create space' }).click()
  await expect(page.getByRole('heading', { name: /Proj/ })).toBeVisible()

  const tag = uniq()
  await page.goto(`/w/${ws}/import`)
  await page
    .getByLabel('Or paste the export')
    .fill(`Title,Status\nAlpha ${tag},Todo\nBeta ${tag},Todo\nGamma ${tag},Todo\n`)
  await page.getByLabel('Title (required)').selectOption('Title')
  await page.getByRole('button', { name: 'Start import' }).click()
  await expect(page.getByRole('status').filter({ hasText: '3 records' })).toBeVisible()

  const jobs = page.getByRole('region', { name: 'Import jobs' })
  await expect(jobs.getByText('done', { exact: true })).toBeVisible({ timeout: 30_000 })
  await expect(jobs.getByText('Imported').locator('xpath=following-sibling::dd')).toHaveText('3')

  const res = await page.request.post(`/api/w/${ws}/commands/item.list`, { data: { limit: 50 } })
  const items = (await res.json()) as { title: string }[]
  expect(items.filter((i) => i.title.endsWith(tag))).toHaveLength(3)
})
