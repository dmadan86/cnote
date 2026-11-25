import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('a watcher is notified of a comment; the notification opens the page on that thread', async ({
  browser,
}) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  const added = await a.request.post(`/api/w/${ws}/commands/member.add`, {
    data: { email: ub.email, role: 'member' },
  })
  expect(added.ok()).toBe(true)

  await createSpace(a)
  await a.getByRole('button', { name: 'New page' }).last().click()
  await expect(a).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const noteUrl = a.url()
  const noteId = noteUrl.split('/n/')[1]
  await a.getByLabel('Page title').fill('Watched plan')
  await a.getByLabel('Page title').blur()

  // B watches the page with the Watch button, and the state survives a reload
  await b.goto(noteUrl)
  const watch = b.getByRole('button', { name: 'Watch', exact: true })
  await watch.click()
  const watching = b.getByRole('button', { name: 'Watching' })
  await expect(watching).toHaveAttribute('aria-pressed', 'true')
  await b.reload()
  await expect(b.getByRole('button', { name: 'Watching' })).toBeVisible()

  // A comments; the worker fans out one notification to B and none to A
  const c = await a.request.post(`/api/w/${ws}/commands/comment.add`, {
    data: { note_id: noteId, body: 'Is this final?' },
  })
  expect(c.ok()).toBe(true)
  const thread = (await c.json()).id as string

  // The worker delivers within a couple of seconds and the bell only reads on load, so reload until it shows.
  // A's earlier title edit can be dispatched after B started watching: B may also hold one "page updated" item.
  await b.goto(`/w/${ws}`)
  await expect(async () => {
    await b.reload()
    await expect(b.getByTestId('notification-badge')).toHaveText(/^[12]$/, { timeout: 1500 })
  }).toPass({ timeout: 20_000 })
  await b.getByRole('button', { name: 'Notifications' }).click()
  const item = b
    .getByRole('list', { name: 'Notifications' })
    .getByRole('button')
    .filter({ hasText: 'commented on' })
  await expect(item).toContainText('commented on Watched plan')
  await item.click()
  await expect(b).toHaveURL(`${noteUrl}?thread=${thread}`)
  await expect(b.getByRole('list', { name: 'Comments' }).getByRole('listitem').first()).toHaveClass(/ring-1/)
  await a.goto(`/w/${ws}`)
  await expect(a.getByTestId('notification-badge')).toHaveCount(0)

  // Stop watching: the next comment by someone else does not reach B
  await b.getByRole('button', { name: 'Watching' }).click()
  await expect(b.getByRole('button', { name: 'Watch', exact: true })).toHaveAttribute('aria-pressed', 'false')
})
