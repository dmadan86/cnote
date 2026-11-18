import { expect, test } from '@playwright/test'
import { signup } from './helpers'

test('Ask: install, ask with a tool, see progress, answer and Sources; Esc closes; history reloads', async ({
  page,
}) => {
  await signup(page)
  await page.getByRole('button', { name: 'Ask your business' }).click()
  const panel = page.getByRole('dialog', { name: 'Ask your business' })
  await expect(panel.getByText("Ask isn't set up")).toBeVisible()
  await panel.getByRole('button', { name: 'Install' }).click()
  await expect(panel.getByText('Ask about your business')).toBeVisible()

  await panel.getByLabel('Your question').fill('/tool home_summary {}')
  await panel.getByRole('button', { name: 'Send' }).click()
  await expect(panel.getByText('/tool home_summary {}')).toBeVisible()
  // The worker answers; the Local provider echoes the tool result.
  await expect(panel.getByText(/Result of home_summary/)).toBeVisible({ timeout: 30_000 })
  await panel.getByText('Sources', { exact: true }).click()
  await expect(panel.locator('summary', { hasText: /home[._]summary/ })).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.screenshot({
    path: '/private/tmp/claude-502/-Users-in45889244-madan-cnote/3facb745-6c61-42f9-b40b-30cfb9a2e904/scratchpad/ag-ask.png',
  })

  await page.keyboard.press('Escape')
  await expect(panel).toHaveCount(0)

  // Cmd/Ctrl+J reopens it with the conversation reloaded from history.
  await page.keyboard.press('ControlOrMeta+j')
  await expect(
    page.getByRole('dialog', { name: 'Ask your business' }).getByText(/Result of home_summary/),
  ).toBeVisible()
})
