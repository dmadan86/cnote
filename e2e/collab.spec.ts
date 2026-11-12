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

  await expect(a.getByRole('list', { name: 'Also editing' })).toContainText(ub.name)
  await expect(b.getByRole('list', { name: 'Also editing' })).toContainText(ua.name)

  await b.getByRole('textbox', { name: 'Page content' }).click()
  await b.keyboard.type('Hello from B')
  await expect(a.getByRole('textbox', { name: 'Page content' })).toContainText('Hello from B')
  await expect(a.locator('.collaboration-carets__label')).toHaveText(ub.name)

  await b.close()
  await expect(a.getByRole('list', { name: 'Also editing' })).toHaveCount(0, { timeout: 40_000 })
})
