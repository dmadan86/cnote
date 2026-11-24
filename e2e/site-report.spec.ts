import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Same environment as site-publish.spec.ts (SITES_DOMAIN, SITES_SERVER_TOKEN, SITES_TEST_SKIP_VERIFY).
// The admin half needs the API to list the signed-up user in CNOTE_PLATFORM_ADMIN_USER_IDS and
// SITES_ABUSE_SCAN=1; user ids are not known before signup, so the e2e env cannot set that.
const port = new URL(process.env.BASE_URL ?? 'http://localhost:3000').port || '3000'

test('Website: a visitor reports a published site', async ({ page, browser }) => {
  await signup(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
  await page.goto(`${base}/site`)
  await page.getByRole('button', { name: 'Use this' }).first().click()
  await page.getByRole('button', { name: 'Publish', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Publish your website' })
  const sub = `e2e-${uniq()}`
  await dialog.getByLabel('Choose your website address').fill(sub)
  await dialog.getByRole('button', { name: 'Publish website' }).click()
  await expect(page.getByRole('dialog', { name: 'Your website is live' })).toBeVisible({ timeout: 30_000 })

  const ctx = await browser.newContext()
  const v = await ctx.newPage()
  await v.goto(`http://${sub}.localhost:${port}/`)
  await v.getByRole('link', { name: 'Report this site' }).click()
  const report = v.getByRole('dialog', { name: 'Report this site' })
  await expect(report.getByRole('button', { name: 'Send report' })).toBeDisabled()
  await report.getByLabel('Scam or fraud').check()
  await report.getByLabel('Details (optional)').fill('Asks for payment up front')
  await report.getByRole('button', { name: 'Send report' }).click()
  await expect(report.getByRole('status')).toContainText('recorded')
  await ctx.close()

  // A person who is not a platform admin gets a clear message, not the queue.
  await page.goto('/account/sites-review')
  await expect(page.getByText('platform administrators only')).toBeVisible()
})

test.fixme('Website: the report shows for a platform admin', async () => {
  // Needs the signed-up user's id in CNOTE_PLATFORM_ADMIN_USER_IDS and SITES_ABUSE_SCAN=1 on the API. Steps: report
  // as above, open /account/sites-review, expect the site in the case list, open it, and see the report under
  // "Visitor reports (not verified)", then Dismiss with a note and confirm.
})
