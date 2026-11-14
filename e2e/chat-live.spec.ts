import { expect, test } from '@playwright/test'
import { signup } from './helpers'

// Chat v2: two browsers see each other's messages, threads and reactions without reloading; agents post as themselves.
test('live messages, threads, reactions and an agent, across two browsers', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: ub.email, role: 'member' } })
    ).ok(),
  ).toBe(true)

  await a.goto(`/w/${ws}/chat`)
  await a.getByRole('button', { name: 'New channel' }).click()
  const dlg = a.getByRole('dialog', { name: 'New channel' })
  await dlg.getByLabel('Name').fill('live')
  await dlg.getByRole('button', { name: 'Create channel' }).click()
  await expect(a.getByRole('heading', { name: '# live' })).toBeVisible()

  // Bob is on another page of the app (no chat open): the sidebar badge goes up on its own
  await b.goto(`/w/${ws}/home`)
  const abox = a.getByLabel('Message', { exact: true })
  await abox.fill('first, no reload')
  await abox.press('Enter')
  await expect(b.getByRole('link', { name: 'Chat, 1 unread' })).toBeVisible({ timeout: 8000 })

  // Bob opens the channel and then sees new messages arrive live
  await b.getByRole('link', { name: /^Chat/ }).click()
  await b.getByRole('button', { name: /live/ }).click()
  const blog = b.getByRole('log', { name: 'Messages' })
  await expect(blog).toContainText('first, no reload')
  await abox.fill('second, still no reload')
  await abox.press('Enter')
  await expect(blog).toContainText('second, still no reload', { timeout: 8000 })
  await expect(b.getByRole('status').filter({ hasText: 'Live updates are paused' })).toHaveCount(0)

  // a reply in a thread: Bob replies, Ann sees the reply count on the parent and the reply in her open thread
  await b
    .getByRole('button', { name: /Reply in thread to .*first, no reload|Reply in thread/ })
    .first()
    .click()
  const bthread = b.getByRole('region', { name: 'Thread' })
  await bthread.getByLabel('Reply', { exact: true }).fill('a thread reply')
  await bthread.getByLabel('Reply', { exact: true }).press('Enter')
  await expect(bthread).toContainText('a thread reply')
  await expect(a.getByRole('button', { name: /1 reply to/ })).toBeVisible({ timeout: 8000 })
  await a.getByRole('button', { name: /1 reply to/ }).click()
  const athread = a.getByRole('region', { name: 'Thread' })
  await expect(athread).toContainText('a thread reply')
  await bthread.getByLabel('Reply', { exact: true }).fill('and one more')
  await bthread.getByLabel('Reply', { exact: true }).press('Enter')
  await expect(athread).toContainText('and one more', { timeout: 8000 })
  await expect(a.getByRole('button', { name: /2 replies to/ })).toBeVisible()

  // reactions: Bob reacts, Ann sees the pill with who reacted; Ann toggles hers and the count follows
  await bthread.getByRole('button', { name: 'Close thread' }).click()
  await b.getByRole('button', { name: 'Add reaction' }).first().click()
  await b.getByRole('button', { name: 'celebrate' }).click()
  const alog = a.getByRole('log', { name: 'Messages' })
  const pill = alog.getByRole('button', { name: /^celebrate, 1:/ })
  await expect(pill).toBeVisible({ timeout: 8000 })
  await expect(pill).toHaveAttribute('aria-label', new RegExp(ub.name))
  await expect(pill).toHaveAttribute('aria-pressed', 'false')
  await pill.click()
  await expect(alog.getByRole('button', { name: /^celebrate, 2:/ })).toHaveAttribute('aria-pressed', 'true')
  await expect(blog.getByRole('button', { name: /^celebrate, 2:/ })).toBeVisible({ timeout: 8000 })

  // an agent: off by default, then allowed, added to the channel, and it posts as itself
  const agent = await (
    await a.request.post(`/api/w/${ws}/commands/agent.create`, {
      data: { name: 'Helper', scopes: ['read', 'write_notes'] },
    })
  ).json()
  const chan = new URL(a.url()).searchParams.get('c')
  const bearer = { headers: { Authorization: `Bearer ${agent.api_key}` } }
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/chat.send`, {
        ...bearer,
        data: { channel_id: chan, body: 'x' },
      })
    ).status(),
  ).toBe(403)
  await a.request.post(`/api/w/${ws}/commands/workspace.update`, {
    data: { settings: { chat_agents: true } },
  })
  await a.reload()
  await a.getByLabel('Add an agent').selectOption({ label: 'Helper' })
  await expect(a.getByText('Helper').first()).toBeVisible()
  const sent = await a.request.post(`/api/w/${ws}/commands/chat.send`, {
    ...bearer,
    data: { channel_id: chan, body: 'beep from the agent' },
  })
  expect(sent.ok()).toBe(true)
  const msg = blog.locator('article', { hasText: 'beep from the agent' })
  await expect(msg).toBeVisible({ timeout: 8000 })
  await expect(msg).toContainText('Agent')
  await expect(msg).toContainText('Helper')
  await expect(msg.getByRole('button', { name: /^Edit message/ })).toHaveCount(0) // not Ann's message, though she owns the agent
})

test('falls back to polling when the stream is refused', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  await a.route('**/chat/stream', (r) => r.fulfill({ status: 503, body: 'no' }))
  await a.request.post(`/api/w/${ws}/commands/chat.channel_create`, { data: { name: 'poll' } })
  await a.goto(`/w/${ws}/chat`)
  await a.getByRole('button', { name: /poll/ }).click()
  await expect(a.getByText('Live updates are paused')).toBeVisible({ timeout: 8000 })
  const chan = new URL(a.url()).searchParams.get('c')
  await a.request.post(`/api/w/${ws}/commands/chat.send`, {
    data: { channel_id: chan, body: 'arrived by polling' },
  })
  await expect(a.getByRole('log', { name: 'Messages' })).toContainText('arrived by polling', {
    timeout: 12_000,
  })
})
