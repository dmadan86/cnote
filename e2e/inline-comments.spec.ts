import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('comment on selected text, reply, resolve; the highlight follows the thread', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)

  const editor = page.getByRole('textbox', { name: 'Page content' })
  const saved = page.waitForResponse((r) => r.url().includes('note.update') && r.ok())
  await editor.click()
  await page.keyboard.type('We ship on Friday after review.')
  await saved

  // Select "Friday": from the end, skip " after review." (14 characters), then select 6 to the left.
  await page.keyboard.press('End')
  for (let i = 0; i < 14; i++) await page.keyboard.press('ArrowLeft')
  for (let i = 0; i < 6; i++) await page.keyboard.press('Shift+ArrowLeft')
  await page
    .getByRole('toolbar', { name: 'Text formatting' })
    .getByRole('button', { name: 'Comment', exact: true })
    .click()
  const box = page.getByRole('form', { name: 'New inline comment' })
  await expect(box).toContainText('Friday')
  await box.getByLabel('Comment on selected text').fill('Which Friday?')
  await box.getByRole('button', { name: 'Comment' }).click()

  const highlight = editor.locator('.cn-comment')
  await expect(highlight).toHaveText('Friday')
  const threads = page.getByRole('list', { name: 'Comments' })
  const thread = threads.getByRole('listitem').filter({ hasText: 'Which Friday?' })
  await expect(thread).toBeVisible()

  await thread.getByLabel('Reply').fill('The 20th')
  await thread.getByLabel('Reply').press('Enter')
  await expect(thread).toContainText('The 20th')

  await page.reload()
  await expect(editor.locator('.cn-comment')).toHaveText('Friday')
  await editor.locator('.cn-comment').click()
  await expect(page.getByRole('list', { name: 'Comments' }).getByRole('listitem').first()).toHaveClass(
    /ring-1/,
  )

  await page.getByRole('list', { name: 'Comments' }).getByRole('button', { name: 'Resolve' }).click()
  await expect(editor.locator('.cn-comment')).toHaveCount(0)
  await page.getByText('Resolved (1)').click()
  await expect(page.getByRole('list', { name: 'Resolved comments' })).toContainText('Which Friday?')
})
