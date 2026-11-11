import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('owner restricts a page, then shares it view-only with a second user', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  await createSpace(a)
  await a.getByRole('button', { name: 'New page' }).last().click()
  await expect(a).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await a.getByLabel('Page title').fill('Quarterly plan')
  await a.getByLabel('Page title').blur()
  const url = a.url()

  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  const added = await a.request.post(`/api/w/${ws}/commands/member.add`, {
    data: { email: ub.email, role: 'member' },
  })
  expect(added.ok()).toBe(true)

  // Unrestricted: a workspace member can open it.
  await b.goto(url)
  await expect(b.getByLabel('Page title')).toHaveValue('Quarterly plan')

  // Owner restricts it to listed people.
  await a.getByRole('button', { name: 'Share' }).click()
  const dlg = a.getByRole('dialog', { name: 'Share page' })
  await dlg.getByLabel('Restriction').selectOption('view')
  await expect(dlg.getByRole('list').first()).toContainText('Tester')
  await dlg.getByRole('button', { name: 'Done' }).click()
  await expect(a.getByLabel('Restricted page')).toBeVisible()

  // The second user can no longer open it.
  await b.goto(url)
  await expect(b.getByText('does not exist or you do not have access')).toBeVisible()
  await expect(b.getByLabel('Page title')).toHaveCount(0)

  // Owner shares it view-only.
  await a.getByRole('button', { name: 'Share' }).click()
  await dlg.getByLabel('Add people').fill(`${ub.name} (${ub.email})`)
  await dlg.getByLabel("New person's access").selectOption('view')
  await dlg.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(dlg.getByLabel(`Access for ${ub.name}`)).toHaveValue('view')

  // Now they can read it, but not edit it.
  await b.goto(url)
  await expect(b.getByLabel('Page title')).toHaveValue('Quarterly plan')
  await expect(b.getByLabel('Page title')).toHaveJSProperty('readOnly', true)
  await expect(b.getByText('You can view this page but not edit it.')).toBeVisible()
  await expect(b.getByLabel('Page content')).toHaveAttribute('contenteditable', 'false')
  // The API agrees.
  const res = await b.request.post(`/api/w/${ws}/commands/note.update`, {
    data: { id: url.split('/n/')[1], title: 'hijacked' },
  })
  expect(res.status()).toBe(403)

  // Upgrading to edit lets them write.
  await dlg.getByLabel(`Access for ${ub.name}`).selectOption('edit')
  await b.reload()
  await expect(b.getByLabel('Page content')).toHaveAttribute('contenteditable', 'true')
})
