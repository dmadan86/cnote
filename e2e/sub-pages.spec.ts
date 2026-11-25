import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('a page lists its child pages under the body, in order, and hides the ones the reader cannot read', async ({
  browser,
}) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  const cmd = async (name: string, data: unknown) =>
    (await a.request.post(`/api/w/${ws}/commands/${name}`, { data })).json()
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  await cmd('member.add', { email: ub.email, role: 'member' })
  await createSpace(a)
  const [space] = await cmd('space.list', {})
  const parent = await cmd('note.create', { space_id: space.id, title: 'Handbook' })
  const lone = await cmd('note.create', { space_id: space.id, title: 'Lone page' })
  const mk = (title: string) => cmd('note.create', { space_id: space.id, title, parent_id: parent.id })
  await mk('Alpha')
  const secret = await mk('Secret')
  await mk('Gamma')
  await cmd('note.restrict', { note_id: secret.id, restriction: 'view' })

  const items = (p: typeof a) => p.getByRole('navigation', { name: 'Sub-pages' }).getByRole('link')
  await a.goto(`/w/${ws}/n/${parent.id}`)
  await expect(items(a)).toHaveText(['Alpha', 'Secret', 'Gamma'])
  await items(a).first().click()
  await expect(a).toHaveURL(/\/n\/[0-9a-f-]+$/)

  await b.goto(`/w/${ws}/n/${parent.id}`)
  await expect(items(b)).toHaveText(['Alpha', 'Gamma'])
  await expect(b.getByText('Secret')).toHaveCount(0)

  await a.goto(`/w/${ws}/n/${lone.id}`)
  await expect(a.getByRole('textbox', { name: 'Page content' })).toBeVisible()
  await expect(a.getByRole('navigation', { name: 'Sub-pages' })).toHaveCount(0)
})
