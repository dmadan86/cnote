import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('two people on one page see each other, their cursor, and their typing live', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  const ua = await signup(a)
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
  await b.goto(a.url())
  // Both editors are on the live (Yjs) connection.
  await expect(a.getByText(/· live/)).toBeVisible()
  await expect(b.getByText(/· live/)).toBeVisible()

  await expect(a.getByRole('list', { name: 'People on this page' })).toContainText(ub.name)
  await expect(b.getByRole('list', { name: 'People on this page' })).toContainText(ua.name)
  await expect(a.getByTestId('viewer-count')).toHaveText('2 viewing')

  // A read-only visitor counts too, marked as viewing.
  const noteId = a.url().split('/n/')[1]
  const restricted = await a.request.post(`/api/w/${ws}/commands/note.restrict`, {
    data: { note_id: noteId, restriction: 'edit' },
  })
  expect(restricted.ok()).toBe(true)
  const c = await (await browser.newContext()).newPage()
  const uc = await signup(c)
  await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: uc.email, role: 'member' } })
  await c.goto(a.url())
  await expect(c.getByText('You can view this page but not edit it.')).toBeVisible()
  await expect(a.getByTestId('viewer-count')).toHaveText('3 viewing')
  await expect(
    a.getByRole('list', { name: 'People on this page' }).getByRole('listitem').filter({ hasText: uc.name }),
  ).toContainText('viewing')
  await c.close()
  await expect(a.getByTestId('viewer-count')).toHaveText('2 viewing')

  await b.getByRole('textbox', { name: 'Page content' }).click()
  await b.keyboard.type('Hello from B')
  await expect(a.getByRole('textbox', { name: 'Page content' })).toContainText('Hello from B')
  await expect(a.locator('.collaboration-carets__label')).toHaveText(ub.name)

  await b.close()
  await expect(a.getByRole('list', { name: 'People on this page' })).toHaveCount(0, { timeout: 40_000 })
})
