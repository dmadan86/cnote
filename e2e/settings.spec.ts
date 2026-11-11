import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

test('api key shown once, agent created, tool group toggled', async ({ page }) => {
  await signup(page)
  await page.getByRole('link', { name: 'Settings' }).first().click()

  await page.getByRole('tab', { name: 'API keys' }).click()
  await page.getByLabel('Key name').fill(`key-${uniq()}`)
  await page.getByRole('button', { name: 'Create API key' }).click()
  const secret = page.getByTestId('secret')
  await expect(secret).toHaveText(/^cn_/)
  await page.getByRole('button', { name: 'Done' }).click()
  await expect(secret).toHaveCount(0)
  await page.reload()
  await page.getByRole('tab', { name: 'API keys' }).click()
  await expect(page.getByTestId('secret')).toHaveCount(0)

  await page.getByRole('tab', { name: 'Agents' }).click()
  const agent = `agent-${uniq()}`
  await page.getByLabel('Agent name').fill(agent)
  await page.getByRole('button', { name: 'Create agent' }).click()
  await expect(page.getByTestId('secret')).toHaveText(/^cn_/)
  await expect(page.getByText(agent, { exact: true })).toBeVisible()

  await page.getByRole('tab', { name: 'Tool groups' }).click()
  const invoices = page.getByRole('checkbox', { name: /manage_invoices/ })
  await expect(invoices).not.toBeChecked()
  const saved = page.waitForResponse((r) => r.url().includes('workspace.update') && r.ok())
  await invoices.check()
  await expect(invoices).toBeChecked()
  await saved // the checkbox is optimistic; reload only after the setting is stored
  await page.reload()
  await page.getByRole('tab', { name: 'Tool groups' }).click()
  await expect(page.getByRole('checkbox', { name: /manage_invoices/ })).toBeChecked()
})
