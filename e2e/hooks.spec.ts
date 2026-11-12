import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

test('admin creates a signed webhook, pauses it and deletes it', async ({ page }) => {
  await signup(page)
  const ws = page.url().match(/\/w\/([0-9a-f-]+)/)?.[1]
  await page.goto(`/w/${ws}/settings`)
  await page.getByRole('tab', { name: 'Webhooks' }).click()

  const name = `Feed ${uniq()}`
  const form = page.getByRole('form', { name: 'New webhook' })
  await form.getByLabel('Name').fill(name)
  await form.getByLabel('Payload URL').fill('https://example.com/cnote-hook')
  await form.getByLabel('Pages').check()
  await form.getByLabel('And/or every (minutes)').fill('60')
  await form.getByRole('button', { name: 'Create webhook' }).click()

  await expect(page.getByTestId('secret')).toHaveText(/^whsec_/)
  await page.getByRole('button', { name: 'Done' }).click()
  const list = page.getByRole('list', { name: 'Webhooks' })
  await expect(list.getByText(name)).toBeVisible()
  await expect(list.getByText('On Pages · every 60 min')).toBeVisible()

  const enabled = list.getByLabel(`${name} enabled`)
  await enabled.click() // controlled: flips once hook.set_enabled returns
  await expect(enabled).not.toBeChecked()
  await page.reload()
  await page.getByRole('tab', { name: 'Webhooks' }).click()
  await expect(page.getByRole('list', { name: 'Webhooks' }).getByLabel(`${name} enabled`)).not.toBeChecked()

  await page.getByRole('button', { name: `Delete ${name}` }).click()
  await expect(page.getByText('No webhooks yet.')).toBeVisible()
})
