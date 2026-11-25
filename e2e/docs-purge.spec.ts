import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

async function setup(page: import('@playwright/test').Page) {
  await signup(page)
  const ws = page.url().split('/w/')[1].split('/')[0]
  const cmd = async (name: string, data: unknown) =>
    (await page.request.post(`/api/w/${ws}/commands/${name}`, { data })).json()
  const [space] = await cmd('space.list', {})
  const mk = (title: string, parent_id?: string) =>
    cmd('note.create', { space_id: space.id, title: `${title} ${uniq()}`, parent_id })
  return { ws, cmd, mk }
}

test('an admin deletes a trashed page for good after a confirmation that names the page count', async ({
  page,
}) => {
  const { ws, cmd, mk } = await setup(page)
  const parent = await mk('Forever')
  await mk('Kid', parent.id)
  const keep = await mk('Keep')
  await cmd('note.delete', { id: parent.id })
  await cmd('note.delete', { id: keep.id })

  await page.goto(`/w/${ws}/trash`)
  await expect(page.getByText('Pages in Trash are deleted for good after 30 days.')).toBeVisible()
  const row = page.getByRole('listitem').filter({ hasText: parent.title })
  await expect(row).toContainText('2 pages')
  await expect(row).toContainText(/Deleted for good in \d+ days/)
  await row.getByRole('button', { name: /Delete forever/ }).click()

  const dlg = page.getByRole('dialog', { name: 'Delete forever' })
  await expect(dlg).toContainText('2 pages')
  await expect(dlg).toContainText('cannot be undone')
  const go = dlg.getByRole('button', { name: 'Delete forever' })
  await expect(go).toBeDisabled()
  await dlg.getByLabel('Type DELETE to confirm').fill('DELETE')
  await go.click()

  await expect(page.getByRole('status').filter({ hasText: 'for good' })).toContainText(parent.title)
  await expect(page.getByRole('listitem').filter({ hasText: parent.title })).toHaveCount(0)
  await expect(page.getByRole('listitem').filter({ hasText: keep.title })).toBeVisible()
  expect(
    (await page.request.post(`/api/w/${ws}/commands/note.get`, { data: { id: parent.id } })).status(),
  ).toBe(404)
})

test('only admins see Delete forever, and the retention setting changes the notice', async ({
  browser,
  page,
}) => {
  const { ws, cmd, mk } = await setup(page)
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  expect(
    (
      await page.request.post(`/api/w/${ws}/commands/member.add`, {
        data: { email: ub.email, role: 'member' },
      })
    ).ok(),
  ).toBe(true)
  const mine = await mk('Member page')
  await cmd('note.delete', { id: mine.id })
  await b.goto(`/w/${ws}/trash`)
  const mrow = b.getByRole('listitem').filter({ hasText: mine.title })
  await expect(mrow.getByRole('button', { name: 'Restore' })).toBeVisible()
  await expect(mrow.getByRole('button', { name: /Delete forever/ })).toHaveCount(0)

  await page.goto(`/w/${ws}/settings`)
  const sel = page.getByLabel('Keep deleted pages for')
  await expect(sel).toHaveValue('30')
  await sel.selectOption('90')
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()
  await page.goto(`/w/${ws}/trash`)
  await expect(page.getByText('Pages in Trash are deleted for good after 90 days.')).toBeVisible()
  await page.goto(`/w/${ws}/settings`)
  await page.getByLabel('Keep deleted pages for').selectOption('0')
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()
  await page.goto(`/w/${ws}/trash`)
  await expect(page.getByText('stay until an admin deletes them for good')).toBeVisible()
})
