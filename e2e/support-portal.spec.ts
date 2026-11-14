import { createServer, type Server } from 'node:net'
import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

// Needs the Go API started with SMTP_URL=smtp://127.0.0.1:$E2E_SMTP_PORT and EMAIL_FROM set: the spec is the mail server.
const port = Number(process.env.E2E_SMTP_PORT ?? 0)
test.skip(!port, 'set E2E_SMTP_PORT (and point the API SMTP_URL at it)')

const mails: string[] = []
let sink: Server
test.beforeAll(async () => {
  sink = createServer((c) => {
    let data = ''
    let inData = false
    c.write('220 sink\r\n')
    c.on('data', (b) => {
      for (const line of b.toString().split('\r\n')) {
        if (inData) {
          if (line === '.') {
            mails.push(data)
            data = ''
            inData = false
            c.write('250 ok\r\n')
          } else data += `${line}\n`
        } else if (line.toUpperCase() === 'DATA') {
          inData = true
          c.write('354 go\r\n')
        } else if (line.toUpperCase() === 'QUIT') c.end('221 bye\r\n')
        else if (line) c.write('250 ok\r\n')
      }
    })
  })
  await new Promise<void>((r) => sink.listen(port, '127.0.0.1', r))
})
test.afterAll(() => sink.close())

test('customer signs in with a magic link, sees public replies only, and replies', async ({
  page,
  browser,
}) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] ?? ''
  const ws = base.replace('/w/', '')
  const call = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), await r.text()).toBeTruthy()
    return r.json()
  }
  const spaces = await call('space.list', {})
  const form = await call('inquiry_form.set', { space_id: spaces[0].id })
  const email = `cust-${uniq()}@example.test`
  const subject = `Printer ${uniq()}`
  const submit = await page.request.post(`/api/public/ticket/${form.token}`, {
    data: { name: 'Meera Rao', phone: '98765 43211', email, subject, message: 'It will not print' },
  })
  expect(submit.ok()).toBeTruthy()

  // settings: portal on, email-in on with a one-time secret
  await page.goto(`${base}/crm/tickets/settings`)
  const channels = page.getByRole('region', { name: 'Email and portal' })
  await channels.getByLabel('Customer portal').click() // saved, then re-read
  await expect(channels.getByLabel('Customer portal')).toBeChecked()
  await expect(channels.getByLabel('Portal link')).toBeVisible()
  await channels.getByLabel('File new email tickets in').selectOption({ index: 1 })
  await channels.getByRole('button', { name: 'Turn on email to ticket' }).click()
  await expect(channels.getByLabel('Signing secret')).toHaveValue(/^[0-9a-f]{64}$/)
  await expect(channels.getByText('cannot be shown again')).toBeVisible()

  // agent: a public reply (emailed) and an internal note
  await page.goto(`${base}/crm/tickets`)
  await page.getByRole('region', { name: 'Tickets' }).getByRole('link', { name: subject }).click()
  await expect(page.getByText('TKT-000001').first()).toBeVisible()
  await page.getByLabel('Message').fill('INTERNAL ONLY: customer is chasing')
  await page.getByRole('button', { name: 'Add note' }).click()
  await expect(page.getByText('Internal note').first()).toBeVisible()
  await page.getByLabel('Public reply').check()
  await page.getByLabel('Message').fill('We are on it')
  await page.getByRole('button', { name: 'Send to customer' }).click()
  await expect(page.getByText('Sent to customer')).toBeVisible()
  await expect.poll(() => mails.some((m) => m.includes(`[TKT-000001] ${subject}`))).toBeTruthy()

  // customer, on a phone
  const ctx = await browser.newContext({ viewport: { width: 375, height: 700 } })
  const v = await ctx.newPage()
  await v.goto(`/portal/${ws}`)
  await v.getByLabel('Email address').fill(email)
  await v.getByRole('button', { name: 'Email me a sign-in link' }).click()
  await expect(v.getByRole('status')).toContainText('sign-in link')
  await expect.poll(() => mails.some((m) => m.includes('/verify#'))).toBeTruthy()
  const mail = mails.find((m) => m.includes('/verify#')) ?? ''
  const link = mail.match(/https?:\/\/\S+\/portal\/[0-9a-f-]+\/verify#\S+/)?.[0] ?? ''
  const url = new URL(link)
  await v.goto(`${url.pathname}${url.hash}`)
  await expect(v.getByRole('heading', { name: /support$/ })).toBeVisible()
  expect(v.url()).not.toContain('#') // the token is gone from the address bar
  const item = v.getByRole('listitem').filter({ hasText: subject })
  await item.getByText(subject).click()
  await expect(item.getByText('We are on it')).toBeVisible()
  await expect(v.getByText('INTERNAL ONLY')).toHaveCount(0)
  expect(await v.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  await item.getByLabel('Reply to TKT-000001').fill('Thanks, still stuck')
  await item.getByRole('button', { name: 'Send reply' }).click()
  await expect(item.locator('p', { hasText: 'Thanks, still stuck' })).toBeVisible()

  // the link works once
  await v.goto(`${url.pathname}${url.hash}`)
  await expect(v.getByRole('heading', { name: 'This link no longer works' })).toBeVisible()
  await ctx.close()

  // the agent sees the customer's reply, and the internal note stays marked internal
  await page.reload()
  await expect(page.locator('p', { hasText: 'Thanks, still stuck' })).toBeVisible()
  await expect(page.getByText('Customer').first()).toBeVisible()
})
