import { expect, test } from '@playwright/test'
import { signup } from './helpers'

test('channels, DMs, mentions, unread, edit/delete and page chips', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  const ua = await signup(a)
  const ws = a.url().split('/w/')[1]
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: ub.email, role: 'member' } })
    ).ok(),
  ).toBe(true)
  // a page to link to
  const space = (await (await a.request.post(`/api/w/${ws}/commands/space.list`, { data: {} })).json())[0].id
  const note = await (
    await a.request.post(`/api/w/${ws}/commands/note.create`, { data: { space_id: space, title: 'Q3 plan' } })
  ).json()

  // the owner creates a public channel and posts
  await a.goto(`/w/${ws}/chat`)
  await a.getByRole('button', { name: 'New channel' }).click()
  const dlg = a.getByRole('dialog', { name: 'New channel' })
  await dlg.getByLabel('Name').fill('launch')
  await dlg.getByRole('button', { name: 'Create channel' }).click()
  const box = a.getByLabel('Message', { exact: true })
  await box.fill(`Plan is here /w/${ws}/n/${note.id}`)
  await box.press('Enter')
  const log = a.getByRole('log', { name: 'Messages' })
  await expect(log.getByRole('link', { name: 'Q3 plan' })).toBeVisible() // a chip, not a raw URL
  await expect(log).not.toContainText(`/w/${ws}/n/`)

  // the member sees the unread badge, opens it, the badge clears
  await b.goto(`/w/${ws}/chat`)
  await expect(b.getByRole('img', { name: '1 unread' })).toBeVisible()
  await b.getByRole('button', { name: /launch/ }).click()
  await expect(b.getByRole('log', { name: 'Messages' })).toContainText('Plan is here')
  await expect(b.getByText('New messages')).toBeVisible()
  await expect(b.getByRole('img', { name: /unread/ })).toHaveCount(0)
  // no Edit/Delete on someone else's message
  await expect(b.getByRole('button', { name: /^Edit message/ })).toHaveCount(0)

  // mention: pick the owner from the @ list; the owner gets a notification and the message arrives by polling
  const bbox = b.getByLabel('Message', { exact: true })
  await bbox.fill('thanks @Tester')
  await b.getByRole('option', { name: new RegExp(ua.name) }).click()
  await bbox.press('Enter')
  await expect(a.getByRole('log', { name: 'Messages' })).toContainText('thanks', { timeout: 15_000 })
  await expect(a.getByRole('link', { name: `@${ua.name}` })).toBeVisible()
  await a.reload()
  await expect(a.getByTestId('notification-badge')).toHaveText('1')
  await a.getByRole('button', { name: 'Notifications' }).click()
  await a.getByRole('list', { name: 'Notifications' }).getByRole('button').first().click()
  await expect(a).toHaveURL(/\/chat\?c=/)

  // edit and delete your own message
  await a.getByRole('button', { name: 'Edit message from', exact: false }).first().click()
  const ed = a.getByLabel('Edit message')
  await ed.fill('Plan is final')
  await a.getByRole('button', { name: 'Save' }).click()
  await expect(a.getByText('(edited)')).toBeVisible()
  await a
    .getByRole('button', { name: /^Delete message/ })
    .first()
    .click()
  await a.getByRole('button', { name: 'Confirm delete' }).click()
  await expect(a.getByText('Message deleted')).toBeVisible()
  await expect(b.getByText('Message deleted')).toBeVisible({ timeout: 15_000 })

  // a direct message is just for the two
  await a.getByRole('button', { name: 'New direct message' }).click()
  await a.getByRole('dialog').getByRole('button', { name: ub.name }).click()
  await a.getByLabel('Message', { exact: true }).fill('psst')
  await a.getByLabel('Message', { exact: true }).press('Enter')
  await expect(b.getByRole('list', { name: 'Direct messages' })).toContainText(ua.name, { timeout: 15_000 })

  // phone width: list and conversation are separate screens
  await b.setViewportSize({ width: 390, height: 800 })
  await b.goto(`/w/${ws}/chat`)
  await expect(b.getByRole('region', { name: 'Conversation' })).toBeHidden()
  await b.getByRole('button', { name: new RegExp(ua.name) }).click()
  await expect(b.getByRole('button', { name: 'Back to conversations' })).toBeVisible()
  await b.getByRole('button', { name: 'Back to conversations' }).click()
  await expect(b.getByRole('list', { name: 'Channels' })).toBeVisible()
})
