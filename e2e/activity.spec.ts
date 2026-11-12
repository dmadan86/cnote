import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('page history reads as sentences with the person’s name, merging repeated edits', async ({ page }) => {
  const u = await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)

  const editor = page.getByRole('textbox', { name: 'Page content' })
  for (const word of ['one', ' two']) {
    const saved = page.waitForResponse((r) => r.url().includes('note.update') && r.ok())
    await editor.click()
    await page.keyboard.press('End')
    await page.keyboard.type(word)
    await saved
  }

  const details = page.getByRole('complementary', { name: 'Page details' })
  if (!(await details.isVisible())) await page.getByRole('button', { name: 'Toggle details panel' }).click()
  await details.getByRole('tab', { name: 'history' }).click()
  await expect(details.getByText(`${u.name} created this page`)).toBeVisible()
  await expect(details.getByText(new RegExp(`^${u.name} edited the content`))).toHaveCount(1)
  await expect(details.getByText(/bindings|note\.updated|user ·/)).toHaveCount(0)
})
