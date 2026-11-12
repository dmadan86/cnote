import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('@mention in a page notifies the mentioned member in-app', async ({ browser }) => {
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
  await a.getByLabel('Page title').fill('Launch plan')
  await a.getByLabel('Page title').blur()

  await a.getByLabel('Page content').click()
  const saved = a.waitForResponse(
    (r) => r.url().includes('/commands/note.update') && (r.request().postData() ?? '').includes('/people/'),
  )
  await a.keyboard.type(`@${ub.name.split(' ')[1]}`)
  const picker = a.getByRole('listbox', { name: 'Mention someone' })
  await picker.getByRole('option', { name: new RegExp(ub.name) }).click()
  expect((await saved).ok()).toBe(true)

  await b.goto(`/w/${ws}`)
  await expect(b.getByTestId('notification-badge')).toHaveText('1')
  await b.getByRole('button', { name: 'Notifications' }).click()
  const item = b.getByRole('list', { name: 'Notifications' }).getByRole('button')
  await expect(item).toContainText('mentioned you in Launch plan')
  await item.click()
  await expect(b).toHaveURL(noteUrl)
  await expect(b.getByTestId('notification-badge')).toHaveCount(0)
})
