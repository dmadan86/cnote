import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

async function setup(page: import('@playwright/test').Page) {
  const u = await signup(page)
  const ws = page.url().split('/w/')[1]
  const cmd = async (name: string, data: unknown) =>
    (await page.request.post(`/api/w/${ws}/commands/${name}`, { data })).json()
  const [space] = await cmd('space.list', {})
  const mk = (title: string, parent_id?: string) =>
    cmd('note.create', { space_id: space.id, title: `${title} ${uniq()}`, parent_id })
  return { u, ws, cmd, space, mk }
}

test('delete a page with a sub-page from the tree, then restore it from Trash', async ({ page }) => {
  const { u, ws, cmd, mk } = await setup(page)
  const parent = await mk('Doomed')
  const child = await mk('Kid', parent.id)
  await page.reload()
  const tree = page.getByRole('tree').first()
  await tree.getByRole('button', { name: `Actions for ${parent.title}` }).click({ force: true })
  await page.getByRole('menuitem', { name: 'Delete' }).click()
  const dlg = page.getByRole('dialog', { name: 'Delete page' })
  await expect(dlg).toContainText('2 pages')
  await dlg.getByRole('button', { name: 'Delete' }).click()
  await expect(tree.getByRole('link', { name: parent.title, exact: true })).toHaveCount(0)

  await page.getByRole('link', { name: 'Trash' }).click()
  await expect(page).toHaveURL(/\/trash$/)
  const row = page.getByRole('listitem').filter({ hasText: parent.title })
  await expect(row).toContainText('2 pages')
  await expect(row).toContainText(`Deleted by ${u.name}`)
  await row.getByRole('button', { name: 'Restore' }).click()
  await expect(row).toContainText('Restored in place')

  expect((await cmd('note.get', { id: child.id })).parent_id).toBe(parent.id)
  await page.goto(`/w/${ws}`)
  await expect(
    page.getByRole('tree').first().getByRole('link', { name: parent.title, exact: true }),
  ).toBeVisible()
})

test('restore lands at the space root when the parent is still in the trash', async ({ page }) => {
  const { cmd, mk } = await setup(page)
  const parent = await mk('Outer')
  const child = await mk('Inner', parent.id)
  await cmd('note.delete', { id: child.id })
  await cmd('note.delete', { id: parent.id })
  await page.goto(`${page.url().split('/w/')[0]}/w/${page.url().split('/w/')[1]}/trash`)
  const row = page.getByRole('listitem').filter({ hasText: child.title })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: 'Restore' }).click()
  await expect(row).toContainText('Restored at the top level of the space')
})

test('a delete that touches a page the caller cannot edit is refused with the generic message', async ({
  browser,
}) => {
  const a = await (await browser.newContext()).newPage()
  const { ws, cmd, mk } = await setup(a)
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: ub.email, role: 'member' } })
    ).ok(),
  ).toBe(true)

  const outer = await mk('Open parent')
  const hidden = await mk('Hidden kid', outer.id)
  await cmd('note.restrict', { note_id: hidden.id, restriction: 'edit' })

  await b.goto(`/w/${ws}/n/${outer.id}`)
  await b.getByRole('button', { name: 'Page actions' }).click()
  await b.getByRole('menuitem', { name: 'Delete' }).click()
  const dlg = b.getByRole('dialog', { name: 'Delete page' })
  await dlg.getByRole('button', { name: 'Delete' }).click()
  await expect(dlg.getByRole('alert')).toHaveText('Some pages here are restricted')
  // Nothing was deleted.
  expect((await cmd('note.get', { id: outer.id })).id).toBe(outer.id)
})

test('a page deleted elsewhere while open shows a notice and stops editing', async ({ page }) => {
  const { ws, cmd, mk } = await setup(page)
  const n = await mk('Live')
  await page.goto(`/w/${ws}/n/${n.id}`)
  await expect(page.getByRole('textbox', { name: 'Page content' })).toBeVisible()
  await cmd('note.delete', { id: n.id })
  await expect(page.getByRole('heading', { name: 'This page was deleted' })).toBeVisible({ timeout: 15000 })
  await expect(page.getByRole('textbox', { name: 'Page content' })).toHaveCount(0)
  await page.getByRole('link', { name: 'Open Trash' }).click()
  await expect(page).toHaveURL(/\/trash$/)
  await expect(page.getByRole('listitem').filter({ hasText: n.title })).toBeVisible()
})
