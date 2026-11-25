import { expect, type Locator, type Page, test } from '@playwright/test'
import { signup, uniq } from './helpers'

async function setup(page: Page) {
  const u = await signup(page)
  const ws = page.url().split('/w/')[1]
  const cmd = async (name: string, data: unknown) =>
    (await page.request.post(`/api/w/${ws}/commands/${name}`, { data })).json()
  const [space] = await cmd('space.list', {})
  const mk = (title: string, parent_id?: string) =>
    cmd('note.create', { space_id: space.id, title: `${title} ${uniq()}`, parent_id })
  return { u, ws, cmd, space, mk }
}

/** Titles of the tree rows, in screen order, limited to the given ones. */
const order = async (page: Page, titles: string[]) =>
  (await page.locator('[data-row-id] a[data-treelink]').allInnerTexts())
    .map((t) => t.trim())
    .filter((t) => titles.includes(t))

async function drag(page: Page, from: Locator, to: Locator, where: 'before' | 'into' | 'after') {
  const a = (await from.boundingBox())!
  const b = (await to.boundingBox())!
  const y = where === 'before' ? b.y + 2 : where === 'after' ? b.y + b.height - 2 : b.y + b.height / 2
  await page.mouse.move(a.x + 40, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(a.x + 40, a.y + a.height / 2 + 12, { steps: 4 })
  await page.mouse.move(b.x + 40, y, { steps: 12 })
  await page.mouse.up()
}

test('drag moves a page into another and reorders, and the order survives a reload', async ({ page }) => {
  const { cmd, mk } = await setup(page)
  const [p1, p2, p3] = [await mk('First'), await mk('Second'), await mk('Third')]
  await page.reload()
  const tree = page.getByRole('tree').first()
  const link = (t: { title: string }) => tree.getByRole('link', { name: t.title, exact: true })
  const titles = [p1.title, p2.title, p3.title]
  await expect.poll(() => order(page, titles)).toEqual(titles) // the tree loads after the page does

  // Drop on the top edge of the first row: reorder.
  await drag(page, link(p3), link(p1), 'before')
  await expect.poll(() => order(page, titles)).toEqual([p3.title, p1.title, p2.title])
  await page.reload()
  await expect.poll(() => order(page, titles)).toEqual([p3.title, p1.title, p2.title])

  // Drop on the middle of a row: becomes its last child.
  await drag(page, link(p2), link(p1), 'into')
  await expect.poll(async () => (await cmd('note.get', { id: p2.id })).parent_id).toBe(p1.id)
  await expect(tree.getByRole('treeitem', { name: p1.title, exact: true })).toHaveAttribute(
    'aria-expanded',
    'true',
  )
  await expect(link(p2)).toBeVisible()
})

test('keyboard: Alt+Arrow reorders, Move up and Move down in the menu, Move... dialog re-parents', async ({
  page,
}) => {
  const { cmd, mk } = await setup(page)
  const [a, b, c] = [await mk('Alpha'), await mk('Beta'), await mk('Gamma')]
  await page.reload()
  const tree = page.getByRole('tree').first()
  const titles = [a.title, b.title, c.title]

  await tree.getByRole('link', { name: b.title, exact: true }).focus()
  await page.keyboard.press('Alt+ArrowUp')
  await expect.poll(() => order(page, titles)).toEqual([b.title, a.title, c.title])
  await expect(page.getByRole('status').filter({ hasText: 'Position 1 of' })).toBeVisible()

  await tree.getByRole('button', { name: `Actions for ${c.title}` }).click({ force: true })
  await page.getByRole('menuitem', { name: 'Move up' }).click()
  await expect.poll(() => order(page, titles)).toEqual([b.title, c.title, a.title])
  await tree.getByRole('button', { name: `Actions for ${b.title}` }).click({ force: true })
  await page.getByRole('menuitem', { name: 'Move down' }).click()
  await expect.poll(() => order(page, titles)).toEqual([c.title, b.title, a.title])

  await tree.getByRole('button', { name: `Actions for ${a.title}` }).click({ force: true })
  await page.getByRole('menuitem', { name: 'Move…' }).click()
  const dlg = page.getByRole('dialog', { name: 'Move page' })
  await dlg.getByLabel('New parent').selectOption(c.id)
  await dlg.getByRole('button', { name: 'Move', exact: true }).click()
  await expect.poll(async () => (await cmd('note.get', { id: a.id })).parent_id).toBe(c.id)
})

test('duplicate opens the copy next to the original', async ({ page }) => {
  const { cmd, mk } = await setup(page)
  const src = await mk('Source')
  await page.reload()
  await page
    .getByRole('tree')
    .first()
    .getByRole('button', { name: `Actions for ${src.title}` })
    .click({ force: true })
  await page.getByRole('menuitem', { name: 'Duplicate' }).click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  expect(page.url()).not.toContain(src.id)
  await expect(page.getByLabel('Page title')).toHaveValue(`${src.title} (copy)`)
  const copy = await cmd('note.get', { id: page.url().split('/n/')[1] })
  expect(copy.parent_id).toBe(src.parent_id)
})

test('delete confirms the number of pages, and the page menu has Move, Duplicate and Delete', async ({
  page,
}) => {
  const { mk } = await setup(page)
  const parent = await mk('Parent')
  await mk('Child', parent.id)
  await page.goto(`/w/${page.url().split('/w/')[1]}/n/${parent.id}`)
  await page.getByRole('button', { name: 'Page actions' }).click()
  for (const n of ['Move…', 'Duplicate', 'Delete'])
    await expect(page.getByRole('menuitem', { name: n })).toBeVisible()
  await page.getByRole('menuitem', { name: 'Delete' }).click()
  const dlg = page.getByRole('dialog', { name: 'Delete page' })
  await expect(dlg).toContainText('2 pages')
  await dlg.getByRole('button', { name: 'Delete' }).click()
  await expect(
    page.getByRole('tree').first().getByRole('link', { name: parent.title, exact: true }),
  ).toHaveCount(0)
})

test('phone: no drag, the Move... dialog works from the menu', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 })
  const { cmd, mk } = await setup(page)
  const [a, b] = [await mk('One'), await mk('Two')]
  await page.reload()
  await page.getByRole('button', { name: 'Spaces' }).click()
  const row = page.getByRole('tree').first()
  await row.getByRole('button', { name: `Actions for ${a.title}` }).click()
  await page.getByRole('menuitem', { name: 'Move…' }).click()
  await page.getByRole('dialog', { name: 'Move page' }).getByLabel('New parent').selectOption(b.id)
  await page
    .getByRole('dialog', { name: 'Move page' })
    .getByRole('button', { name: 'Move', exact: true })
    .click()
  await expect.poll(async () => (await cmd('note.get', { id: a.id })).parent_id).toBe(b.id)
})
