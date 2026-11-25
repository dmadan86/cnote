import { execFileSync } from 'node:child_process'
import { expect, test } from '@playwright/test'
import { signup } from './helpers'
import { gridOf, rowWith, templateBase, wsOf } from './tables-helpers'

test('a read-only member sees "View only" and gets no editors', async ({ page, browser }) => {
  const { url } = await templateBase(page)
  const ws = wsOf(page).replace('/w/', '')

  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  const added = await page.request.post(`/api/w/${ws}/commands/member.add`, {
    data: { email: ub.email, role: 'member' },
  })
  expect(added.ok()).toBe(true)
  // there is no UI for a space viewer yet: make the member one of the space viewers in the database
  execFileSync(
    'psql',
    [
      process.env.DATABASE_ADMIN_URL ?? '',
      '-qc',
      `insert into core.space_members (workspace_id, space_id, user_id, role)
       select s.workspace_id, s.id, u.id, 'viewer' from core.spaces s, core.users u
       where s.workspace_id = '${ws}' and u.email = '${ub.email}'`,
    ],
    { stdio: 'pipe' },
  )

  await b.goto(url)
  await expect(b.getByText('View only')).toBeVisible()
  const grid = gridOf(b)
  await expect(grid).toHaveAttribute('aria-readonly', 'true')
  await expect(b.getByText('10 records')).toBeVisible()
  // nothing that edits is offered
  for (const name of ['Add field', 'Add table', 'Import CSV', 'Rename table', 'Edit view']) {
    await expect(b.getByRole('button', { name })).toHaveCount(0)
  }
  await expect(b.getByRole('toolbar', { name: 'View settings' })).toHaveCount(0)
  await expect(b.getByRole('button', { name: 'Add a record' })).toHaveCount(0)

  // a cell never turns into an editor, by double click or by typing
  const cell = rowWith(b, 'Steel utensils, 40 sets').getByRole('gridcell', {
    name: 'Steel utensils, 40 sets',
  })
  await cell.dblclick()
  await b.keyboard.type('hijacked')
  await b.keyboard.press('Enter')
  await expect(grid.getByRole('textbox')).toHaveCount(0)
  await expect(cell).toHaveText('Steel utensils, 40 sets')

  // the API agrees
  const t = await b.request.post(`/api/w/${ws}/commands/base.list`, { data: {} })
  const base = (await t.json()).find((x: { id: string }) => url.includes(x.id))
  const detail = await (
    await b.request.post(`/api/w/${ws}/commands/base.get`, { data: { id: base.id } })
  ).json()
  expect(detail.role).toBe('read')
  const w = await b.request.post(`/api/w/${ws}/commands/field.create`, {
    data: { table_id: detail.tables[0].id, name: 'Nope', type: 'text' },
  })
  expect(w.status()).toBe(403)
})
