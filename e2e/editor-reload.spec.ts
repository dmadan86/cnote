import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

// A page typed on and reloaded before the gateway's ~2 s snapshot debounce must not come back with its text twice.
for (const delay of [500, 1500]) {
  test(`typing then reloading after ${delay} ms keeps the text once`, async ({ page }) => {
    await signup(page)
    await createSpace(page)
    await page.getByRole('button', { name: 'New page' }).last().click()
    await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
    const box = page.getByRole('textbox', { name: 'Page content' })
    await expect(page.getByText(/· live/)).toBeVisible()
    await box.click()
    await page.keyboard.type('Mark word')
    await page.waitForTimeout(delay)
    for (let i = 0; i < 2; i++) {
      await page.reload()
      await expect(page.getByText(/· live/)).toBeVisible()
      await expect(box).toContainText('Mark word')
      expect(await box.innerText()).toBe('Mark word')
      await expect(box.locator('p')).toHaveCount(1)
    }
  })
}
