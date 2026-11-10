import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('typing then navigating away immediately still saves', async ({ page }) => {
  await signup(page)
  const space = await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const noteUrl = page.url()

  const word = `flush${uniq()}`
  await page.getByRole('textbox', { name: 'Page content' }).click()
  await page.keyboard.type(`Typed ${word}`)
  // Leave inside the 700ms debounce window.
  await page.getByRole('link', { name: space }).first().click()
  await expect(page).toHaveURL(/\/s\//)

  await page.goto(noteUrl)
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText(`Typed ${word}`)
})
