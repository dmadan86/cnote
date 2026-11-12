import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('@mention in a comment notifies; hovering a person shows their card; the card opens their profile', async ({
  browser,
}) => {
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
  await a.reload() // pick up the new member in the shell

  const box = a.getByRole('textbox', { name: 'Add a comment' })
  await box.click()
  await box.pressSequentially(`Please review @${ub.name.split(' ')[1]}`)
  await a
    .getByRole('listbox', { name: 'Mention someone' })
    .getByRole('option', { name: new RegExp(ub.name) })
    .click()
  await expect(box).toHaveValue(`Please review @${ub.name} `)
  await a.getByRole('button', { name: 'Comment', exact: true }).last().click()

  const thread = a.getByRole('list', { name: 'Comments' })
  const mention = thread.getByRole('link', { name: `@${ub.name}` })
  await expect(mention).toBeVisible()

  // Hover the mention: the profile card.
  await mention.hover()
  const card = a.getByRole('dialog', { name: `Profile: ${ub.name}` })
  await expect(card).toContainText(ub.email)
  await expect(card).toContainText('member')
  // Hover the comment's author: their card.
  await a.mouse.move(0, 0)
  await expect(card).toHaveCount(0)
  await thread.locator('[data-person-id]').filter({ hasText: ua.name }).first().hover()
  await expect(a.getByRole('dialog', { name: `Profile: ${ua.name}` })).toBeVisible()

  // B was notified, and the profile page works.
  await b.goto(`/w/${ws}`)
  await expect(b.getByTestId('notification-badge')).toHaveText('1')
  await a.mouse.move(0, 0)
  await mention.hover()
  await card.getByRole('link', { name: 'View profile' }).click()
  await expect(a.getByRole('heading', { name: ub.name })).toBeVisible()
  await expect(a.getByRole('region', { name: 'Assigned work' })).toContainText('Nothing open.')
})
