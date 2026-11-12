import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('page header buttons stay on screen at narrow widths', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await page.setViewportSize({ width: 700, height: 800 })
  for (const name of ['Sub-page', 'Toggle details panel']) {
    const b = page.getByRole('button', { name })
    await expect(b).toBeInViewport({ ratio: 1 })
  }
})
