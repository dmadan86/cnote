import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

async function pageWithTitle(page: import('@playwright/test').Page, title: string) {
  const before = page.url() // a second page: wait for the URL to move, or the title lands on the first page
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(page).not.toHaveURL(before)
  const t = page.getByRole('textbox', { name: 'Page title' })
  await t.fill(title)
  await t.press('Enter')
  await expect(page.getByRole('link', { name: title, exact: true }).first()).toBeVisible()
}

test('status, emoji and wide page', async ({ page }) => {
  await page.setViewportSize({ width: 1800, height: 900 }) // room beside the sidebar and the side panel to grow
  await signup(page)
  await createSpace(page)
  await pageWithTitle(page, 'Policy')

  await page.getByRole('button', { name: /Page status/ }).click()
  await page.getByRole('menuitemradio', { name: 'In review' }).click()
  await expect(page.getByRole('button', { name: 'Page status: In review' })).toBeVisible()

  await page.getByRole('button', { name: 'Add page emoji' }).click()
  await page.getByRole('dialog', { name: 'Choose an emoji' }).getByRole('button', { name: '🔥' }).click()
  await expect(page.getByRole('button', { name: /Page emoji 🔥/ })).toBeVisible()
  await expect(page.getByRole('navigation').or(page.locator('aside')).getByText('🔥').first()).toBeVisible()

  const article = page.locator('article')
  const before = (await article.boundingBox())?.width ?? 0
  await page.getByRole('button', { name: 'Wide page' }).click()
  await expect(page.getByRole('button', { name: 'Wide page' })).toHaveAttribute('aria-pressed', 'true')
  await expect.poll(async () => (await article.boundingBox())?.width ?? 0).toBeGreaterThan(before)

  await page.reload()
  await expect(page.getByRole('button', { name: 'Page status: In review' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Wide page' })).toHaveAttribute('aria-pressed', 'true')
})

test('labels, label filter and search filters', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await pageWithTitle(page, 'Refund policy')
  const labels = page.getByRole('group', { name: 'Labels' })
  await labels.getByRole('button', { name: 'Add label' }).click()
  await labels.getByLabel('New label').fill('Finance')
  await labels.getByLabel('New label').press('Enter')
  await expect(labels.getByRole('link', { name: 'finance' })).toBeVisible() // lower-cased

  await pageWithTitle(page, 'Holiday list')
  await page.getByRole('group', { name: 'Labels' }).getByRole('button', { name: 'Add label' }).click()
  await page.getByLabel('New label').fill('hr')
  await page.getByLabel('New label').press('Enter')

  await page.getByRole('link', { name: 'Refund policy', exact: true }).first().click()
  await page.getByRole('group', { name: 'Labels' }).getByRole('link', { name: 'finance' }).click()
  await expect(page).toHaveURL(/search\?label=finance/)
  await expect(page.getByRole('heading', { name: 'Pages labelled "finance"' })).toBeVisible()
  await expect(page.getByRole('main').getByRole('link', { name: 'Refund policy' })).toBeVisible()
  await expect(page.getByRole('main').getByRole('link', { name: 'Holiday list' })).toHaveCount(0)

  // remove the label again
  await page.getByRole('main').getByRole('link', { name: 'Refund policy' }).click()
  await page.getByRole('button', { name: 'Remove label finance' }).click()
  await expect(page.getByRole('link', { name: 'finance' })).toHaveCount(0)

  // status filter
  await page.getByRole('button', { name: /Page status/ }).click()
  await page.getByRole('menuitemradio', { name: 'Final' }).click()
  await expect(page.getByRole('button', { name: 'Page status: Final' })).toBeVisible()
  await page.goto(page.url().replace(/\/n\/.*/, '/search?status=final'))
  await expect(page.getByRole('main').getByRole('link', { name: 'Refund policy' })).toBeVisible()
  await expect(page.getByRole('main').getByRole('link', { name: 'Holiday list' })).toHaveCount(0)
  await page.getByLabel('Status', { exact: true }).selectOption('')
  await expect(page.getByText('Search', { exact: true }).first()).toBeVisible()
})

test('star, unstar and recent in the sidebar', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await pageWithTitle(page, 'Handbook')
  await page.getByRole('button', { name: 'Star page' }).click()
  const starred = page.getByTestId('mine-starred')
  await expect(starred.getByRole('link', { name: 'Handbook' })).toBeVisible()

  // an opening is recorded after a short stay
  await expect(page.getByTestId('mine-recent').getByRole('link', { name: 'Handbook' })).toBeVisible({
    timeout: 10_000,
  })

  await page.getByRole('button', { name: 'Unstar page' }).click()
  await expect(starred).toHaveCount(0)
})

test('page header wraps on a phone', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await pageWithTitle(page, 'Mobile page')
  await page.setViewportSize({ width: 390, height: 800 })
  for (const name of [/Page status/, 'Star page', 'Toggle details panel']) {
    await expect(page.getByRole('button', { name })).toBeInViewport({ ratio: 1 })
  }
})
