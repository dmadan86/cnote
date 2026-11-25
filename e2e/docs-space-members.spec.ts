import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('a private space: add a viewer, promote, remove; then archive', async ({ browser }) => {
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

  const name = await createSpace(a)
  const spaceId = a.url().split('/s/')[1]
  const made = await a.request.post(`/api/w/${ws}/commands/note.create`, {
    data: { space_id: spaceId, title: 'Runbook', content_md: 'restart it' },
  })
  const noteId = (await made.json()).id as string
  const bGet = () => b.request.post(`/api/w/${ws}/commands/note.get`, { data: { id: noteId } })
  const bEdit = () =>
    b.request.post(`/api/w/${ws}/commands/note.update`, { data: { id: noteId, title: 'B was here' } })

  // open to private, with the count of people who lose access
  await a.getByRole('link', { name: 'Space settings' }).click()
  const dialog = a.getByRole('dialog', { name: 'Space settings' })
  await expect(dialog.getByText('Everyone in the workspace can edit this space')).toBeVisible()
  await dialog.getByLabel('Visibility').selectOption('private')
  await dialog.getByRole('button', { name: 'Save details' }).click()
  await expect(dialog.getByRole('alert')).toContainText('1 workspace member loses access')
  await dialog.getByRole('button', { name: 'Make private' }).click()
  await expect(dialog.getByText('Only the people listed here can open this space.')).toBeVisible()
  expect((await bGet()).ok()).toBe(false)

  // add as viewer: reads, cannot edit
  await dialog.getByLabel('Add people').selectOption({ label: `${ub.name} (${ub.email})` })
  await dialog.getByLabel('Role for new member').selectOption('viewer')
  await dialog.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(dialog.getByLabel(`Role for ${ub.name}`)).toHaveValue('viewer')
  expect((await bGet()).ok()).toBe(true)
  expect((await bEdit()).ok()).toBe(false)

  // promote to editor
  await dialog.getByLabel(`Role for ${ub.name}`).selectOption('editor')
  await expect(dialog.getByLabel(`Role for ${ub.name}`)).toHaveValue('editor')
  expect((await bEdit()).ok()).toBe(true)

  // remove
  await dialog.getByRole('button', { name: `Remove ${ub.name}` }).click()
  // the role select is also absent while the change is in flight, so poll the server instead of the list
  await expect.poll(async () => (await bGet()).ok()).toBe(false)

  // archive: read-only, and a way back
  a.on('dialog', (d) => d.accept())
  await dialog.getByRole('button', { name: 'Archive space' }).click()
  await expect(dialog.getByRole('status')).toContainText('This space is archived')
  const refused = await a.request.post(`/api/w/${ws}/commands/note.update`, {
    data: { id: noteId, title: 'late' },
  })
  expect((await refused.json()).error.message).toBe('This space is archived')
  await dialog.getByRole('button', { name: 'Unarchive space' }).click()
  await expect(dialog.getByRole('button', { name: 'Archive space' })).toBeVisible()
  await dialog.getByRole('button', { name: 'Close' }).click()
  await expect(a.getByRole('heading', { name })).toBeVisible()
})
