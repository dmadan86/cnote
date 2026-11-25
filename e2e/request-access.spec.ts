import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('a member without access asks for it and the owner is notified', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  await createSpace(a)
  await a.getByRole('button', { name: 'New page' }).last().click()
  await expect(a).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await a.getByLabel('Page title').fill('Salary bands')
  await a.getByLabel('Page title').blur()
  const url = a.url()

  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  const added = await a.request.post(`/api/w/${ws}/commands/member.add`, {
    data: { email: ub.email, role: 'member' },
  })
  expect(added.ok()).toBe(true)

  await a.getByRole('button', { name: 'Share' }).click()
  const dlg = a.getByRole('dialog', { name: 'Share page' })
  await dlg.getByLabel('Restriction').selectOption('view')
  await dlg.getByRole('button', { name: 'Done' }).click()

  // The second person cannot open it and asks for access; the screen never says whether the page exists.
  await b.goto(url)
  await expect(b.getByText('does not exist or you do not have access')).toBeVisible()
  await b.getByLabel('Message to the people who can grant access').fill('Need it for planning')
  await b.getByRole('button', { name: 'Request access' }).click()
  await expect(b.locator('p[role=status]')).toContainText(
    'If this page exists, the people who can grant access have been asked',
  )
  await expect(b.locator('p[role=status]')).toContainText('ask your workspace admin for access')

  // The owner sees it in the bell, with a Share button.
  await a.goto(`/w/${ws}`)
  await a.getByRole('button', { name: 'Notifications' }).click()
  await expect(a.getByRole('list', { name: 'Notifications' })).toContainText(
    'asked for access to Salary bands',
  )
  await a.getByRole('link', { name: 'Share', exact: true }).click()
  // It opens the share dialog with the requester filled in.
  await expect(a).toHaveURL(new RegExp(`${url}\\?share=`))
  await expect(a.getByRole('dialog', { name: 'Share page' }).getByLabel('Add people')).toHaveValue(
    new RegExp(ub.email.replace(/[.+]/g, '\\$&')),
  )
})
