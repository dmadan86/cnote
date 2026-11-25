import { expect, type Page, test } from '@playwright/test'
import { newUser, signup, uniq } from './helpers'

// Email invites (docs/design/onboarding.md 6.6). The e2e server has no SMTP, so the admin gets a copyable link, shown once.

const wsOf = (page: Page) => new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string

async function invite(page: Page, email: string, role = 'member') {
  await page.goto(`/w/${wsOf(page)}/settings?tab=Members`)
  await page.getByLabel('Email addresses').fill(email)
  await page.getByLabel('Role').selectOption(role)
  await page.getByRole('button', { name: 'Send invitations' }).click()
  const link = page.getByLabel(`Invitation link for ${email}`)
  await expect(link).toBeVisible()
  return link.inputValue()
}

test('invite a new person: they set a password and land in the workspace', async ({ page, browser }) => {
  await signup(page)
  const ws = wsOf(page)
  const email = `inv-${uniq()}@example.test`
  const url = await invite(page, email)
  await expect(page.getByText('It will not be shown again.')).toBeVisible()
  const pending = page.getByRole('region', { name: 'Pending invitations' })
  await expect(pending.getByText(email)).toBeVisible()
  await expect(pending.getByText('Expires in 7 days')).toBeVisible()
  await expect(pending.getByRole('button', { name: `Resend invitation to ${email}` })).toBeVisible()

  // a phone-sized visitor with no account
  const ctx = await browser.newContext({ viewport: { width: 375, height: 700 } })
  const guest = await ctx.newPage()
  const res = await guest.goto(url)
  expect(res?.headers()['referrer-policy']).toBe('no-referrer')
  await expect(guest.getByRole('heading', { name: /^Join WS / })).toBeVisible()
  await expect(guest.getByText(email)).toBeVisible()
  expect(await guest.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  const pw = `pw-${uniq()}${uniq()}`
  await guest.getByLabel('Your name').fill('New Teammate')
  await guest.getByLabel('Choose a password').fill(pw)
  await guest.getByRole('button', { name: 'Create account and join' }).click()
  await expect(guest).toHaveURL(new RegExp(`/w/${ws}$`))
  await expect(guest.getByRole('button', { name: 'User menu' })).toBeVisible()

  // the link is spent
  await guest.goto(url)
  await expect(guest.getByText('not valid any more')).toBeVisible()
  await expect(guest.getByLabel('Choose a password')).toHaveCount(0)
  await ctx.close()

  // the owner sees them as a member and the invitation gone
  await page.goto(`/w/${ws}/settings?tab=Members`)
  await expect(page.getByText('New Teammate')).toBeVisible()
  await expect(page.getByText('No pending invitations.')).toBeVisible()
})

test('invite an existing account: sign in as that address, then accept', async ({ page, browser }) => {
  await signup(page)
  const ws = wsOf(page)
  const ctx = await browser.newContext()
  const other = await ctx.newPage()
  const u = newUser()
  await signup(other, u)
  await other.request.post('/api/auth/logout')
  await ctx.clearCookies()

  const url = await invite(page, u.email, 'admin')
  await other.goto(url)
  await expect(other.getByLabel('Email')).toHaveValue(u.email)
  await other.getByLabel('Password').fill(`wrong-${uniq()}`)
  await other.getByRole('button', { name: 'Sign in to join' }).click()
  await expect(other.getByRole('alert')).toBeVisible()
  await other.getByLabel('Password').fill(u.password)
  await other.getByRole('button', { name: 'Sign in to join' }).click()
  await other.getByRole('button', { name: /^Join WS / }).click()
  await expect(other).toHaveURL(new RegExp(`/w/${ws}$`))
  const me = await other.request.get('/api/auth/me')
  expect(
    (await me.json()).workspaces.some((w: { id: string; role: string }) => w.id === ws && w.role === 'admin'),
  ).toBe(true)
  await ctx.close()
})

test('revoke: the link stops working and the list empties', async ({ page, browser }) => {
  await signup(page)
  const email = `inv-${uniq()}@example.test`
  const url = await invite(page, email)
  await page.getByRole('button', { name: `Revoke invitation to ${email}` }).click()
  await expect(page.getByText('No pending invitations.')).toBeVisible()
  const ctx = await browser.newContext()
  const guest = await ctx.newPage()
  await guest.goto(url)
  await expect(guest.getByText('not valid any more')).toBeVisible()
  await expect(guest.getByLabel('Choose a password')).toHaveCount(0)
  await ctx.close()
})

test('resend rotates the link and the old one stops working', async ({ page, browser }) => {
  await signup(page)
  const email = `inv-${uniq()}@example.test`
  const first = await invite(page, email)
  await page.getByRole('button', { name: `Resend invitation to ${email}` }).click()
  const box = page.getByLabel(`Invitation link for ${email}`)
  await expect(box).not.toHaveValue(first)
  const second = await box.inputValue()
  const ctx = await browser.newContext()
  const guest = await ctx.newPage()
  await guest.goto(first)
  await expect(guest.getByText('not valid any more')).toBeVisible()
  await guest.goto(second)
  await expect(guest.getByLabel('Choose a password')).toBeVisible()
  await ctx.close()
})

test('the Get set up guide ticks "Invite your team" once someone accepted', async ({ page, browser }) => {
  test.skip(process.env.HOME_WIDGETS !== '1', 'needs HOME_WIDGETS=1')
  await signup(page)
  const ws = wsOf(page)
  const guide = page.getByRole('region', { name: 'Get set up' })
  await guide.getByRole('button', { name: /Show all/ }).click()
  await expect(guide.getByRole('link', { name: /To do: Invite your team/ })).toBeVisible()
  const email = `inv-${uniq()}@example.test`
  const url = await invite(page, email)
  const ctx = await browser.newContext()
  const guest = await ctx.newPage()
  await guest.goto(url)
  await guest.getByLabel('Your name').fill('Teammate')
  await guest.getByLabel('Choose a password').fill(`pw-${uniq()}${uniq()}`)
  await guest.getByRole('button', { name: 'Create account and join' }).click()
  await expect(guest).toHaveURL(new RegExp(`/w/${ws}$`))
  await ctx.close()
  await page.goto(`/w/${ws}`)
  await guide.getByRole('button', { name: /Show all/ }).click()
  await expect(guide.getByRole('link', { name: /Done: Invite your team/ })).toBeVisible()
})
