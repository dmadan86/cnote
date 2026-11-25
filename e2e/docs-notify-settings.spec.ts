import { expect, test } from '@playwright/test'
import { signup } from './helpers'

// Docs B1, P3: email settings. Mail itself is checked against the SMTP sink in the Go tests (apps/api/internal/worker).

test('email settings save at once, survive a reload and show the unsubscribed state', async ({ page }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  await page.goto(`/w/${ws}/settings?tab=Notifications`)
  await expect(page.getByRole('heading', { name: 'Notifications', level: 2 })).toBeVisible()

  // defaults: smart mode, digest at 08:00
  const smart = page.getByRole('radio', { name: /Smart/ })
  await expect(smart).toBeChecked()
  await expect(page.getByLabel('Digest time (India Standard Time)')).toHaveValue('8')

  await page.getByRole('radio', { name: /Daily digest only/ }).check()
  await expect(page.getByRole('status').filter({ hasText: 'Saved.' })).toBeVisible()
  await page.getByLabel('Digest time (India Standard Time)').selectOption('18')
  await expect(page.getByRole('status').filter({ hasText: 'Saved.' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('radio', { name: /Daily digest only/ })).toBeChecked()
  await expect(page.getByLabel('Digest time (India Standard Time)')).toHaveValue('18')

  // "No email" turns the type controls off
  await page.getByRole('radio', { name: /No email/ }).check()
  await expect(page.getByRole('checkbox', { name: /Daily digest/ })).toBeDisabled()
  await page.getByRole('radio', { name: /Smart/ }).check()

  // what the one-click unsubscribe link does: the page says so, and one click turns it back on
  const set = await page.request.post(`/api/w/${ws}/commands/notify_prefs.set`, {
    data: { direct_off: true },
  })
  expect(set.ok()).toBe(true)
  await page.reload()
  await expect(page.getByText('You unsubscribed from some of these emails.')).toBeVisible()
  const quick = page.getByRole('checkbox', { name: /Quick emails/ })
  await expect(quick).not.toBeChecked()
  await quick.check()
  await expect(page.getByText('You unsubscribed from some of these emails.')).toBeHidden()
})

test('the bell links to the email settings', async ({ page }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  await page.getByRole('button', { name: 'Notifications' }).click()
  await page.getByRole('link', { name: 'Email settings' }).click()
  await expect(page).toHaveURL(new RegExp(`/w/${ws}/settings\\?tab=Notifications`))
})
