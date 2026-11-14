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
  // Leave inside the 700ms debounce window. The page sends the pending save on the way out; the reload below must
  // wait for the server to acknowledge it, or it can read the page before the save commits.
  const saved = page.waitForResponse((r) => r.url().includes('note.update') && r.ok())
  await page.getByRole('link', { name: space }).first().click()
  await expect(page).toHaveURL(/\/s\//)
  await saved

  await page.goto(noteUrl)
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText(`Typed ${word}`)
})
