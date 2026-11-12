import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Needs the API started with CNOTE_PLATFORM_ADMINS=@example.test (signup emails use that domain).
test('publish, review, install, call with the app token, uninstall', async ({ page }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const name = `Partner ${uniq()}`
  await page.goto(`/w/${ws}/marketplace`)
  await page.getByRole('tab', { name: 'Build apps' }).click()
  await page.getByRole('button', { name: 'New app' }).click()
  const form = page.getByRole('dialog', { name: 'New app' })
  await form.getByLabel('App name').fill(name)
  await form.getByLabel('Slug').fill(`partner-${uniq()}`)
  await form.getByLabel('Tagline').fill('Syncs things')
  await form.getByLabel('Description').fill('A partner app used by the e2e suite.')
  await form.getByLabel('Redirect URL').fill('https://partner-app.test/callback')
  await form.getByRole('button', { name: 'Create app' }).click()
  const clientId = (await page.getByTestId('client-id').textContent()) ?? ''
  const secret = (await page.getByTestId('secret').textContent()) ?? ''
  expect(clientId).toMatch(/^[0-9a-f-]{36}$/)
  expect(secret).toMatch(/^cnas_/)
  await page.getByRole('button', { name: 'Submit for review' }).click()

  await page.getByRole('tab', { name: 'Review' }).click()
  const approve = page.getByRole('button', { name: `Approve ${name}` })
  await approve.click()
  await expect(approve).toHaveCount(0) // approved and dropped from the queue

  await page.getByRole('tab', { name: 'Browse' }).click()
  await page.getByLabel('Search apps').fill(name)
  await page.route('https://partner-app.test/**', (r) =>
    r.fulfill({ status: 200, contentType: 'text/html', body: '<h1>Partner app</h1>' }),
  )
  await page.getByRole('button', { name, exact: true }).click()
  const dlg = page.getByRole('dialog', { name })
  await dlg.getByRole('button', { name: 'Install', exact: true }).click()
  await page.waitForURL(/partner-app\.test\/callback\?/)
  const code = new URL(page.url()).searchParams.get('code')
  expect(code).toMatch(/^cnic_/)

  const tok = await page.request.post('/api/marketplace/token', {
    data: { client_id: clientId, client_secret: secret, code },
  })
  expect(tok.status()).toBe(200)
  const { access_token } = await tok.json()
  const call = (c: string) =>
    page.request.post(`/api/w/${ws}/commands/${c}`, {
      data: {},
      headers: { authorization: `Bearer ${access_token}`, cookie: '' },
    })
  expect((await call('space.list')).status()).toBe(200)
  expect((await call('invoice.issue')).status()).toBe(403)

  await page.goto(`/w/${ws}/marketplace`)
  await page.getByLabel('Search apps').fill(name)
  await page.getByRole('button', { name, exact: true }).click()
  await page.getByRole('dialog', { name }).getByRole('button', { name: 'Uninstall' }).click()
  await expect(page.getByRole('dialog', { name }).getByRole('button', { name: 'Install', exact: true })).toBeVisible()
  expect((await call('space.list')).status()).toBe(401)
})

test('uninstalling the official Whiteboard app hides its nav link', async ({ page }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const nav = page.getByRole('navigation', { name: 'Modules' })
  await expect(nav.getByRole('link', { name: 'Whiteboard' })).toBeVisible()
  await page.goto(`/w/${ws}/marketplace`)
  await page.getByRole('button', { name: 'Whiteboard', exact: true }).click()
  const dlg = page.getByRole('dialog', { name: 'Whiteboard' })
  await dlg.getByRole('button', { name: 'Uninstall' }).click()
  await expect(nav.getByRole('link', { name: 'Whiteboard' })).toHaveCount(0)
  await dlg.getByRole('button', { name: 'Install', exact: true }).click()
  await expect(nav.getByRole('link', { name: 'Whiteboard' })).toBeVisible()
})
