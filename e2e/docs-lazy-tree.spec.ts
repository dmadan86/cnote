import { expect, type Page, test } from '@playwright/test'
import { signup, uniq } from './helpers'

async function setup(page: Page) {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const cmd = async (name: string, data: unknown) =>
    (await page.request.post(`/api/w/${ws}/commands/${name}`, { data })).json()
  const [space] = await cmd('space.list', {})
  return { ws, cmd, space }
}

test('a long level loads 100 rows, then Show more adds the rest', async ({ page }) => {
  test.setTimeout(120_000)
  const { cmd, space } = await setup(page)
  const tag = uniq()
  for (let i = 0; i < 130; i += 4)
    await Promise.all(
      [0, 1, 2, 3]
        .filter((j) => i + j < 130)
        .map((j) =>
          cmd('note.create', { space_id: space.id, title: `Lazy ${tag} ${String(i + j).padStart(3, '0')}` }),
        ),
    )
  await page.reload()
  const tree = page.getByRole('tree').first()
  const rows = tree.locator(`a[data-treelink]`, { hasText: `Lazy ${tag}` })
  await expect(tree.getByRole('button', { name: 'Show more' })).toBeVisible()
  expect(await rows.count()).toBeLessThanOrEqual(100)
  await tree.getByRole('button', { name: 'Show more' }).click()
  await expect(rows).toHaveCount(130)
  await expect(tree.getByRole('button', { name: 'Show more' })).toHaveCount(0)
})

test('opening a deep page reveals its path, and the page menu opens find and replace', async ({ page }) => {
  const { ws, cmd, space } = await setup(page)
  let parent: string | undefined
  const titles: string[] = []
  for (let d = 0; d < 4; d++) {
    const title = `Deep ${d} ${uniq()}`
    const n = await cmd('note.create', { space_id: space.id, title, parent_id: parent })
    titles.push(title)
    parent = n.id
  }
  await page.goto(`/w/${ws}/n/${parent}`)
  const tree = page.getByRole('tree').first()
  for (const t of titles) await expect(tree.getByRole('link', { name: t, exact: true })).toBeVisible()
  await expect(tree.getByRole('treeitem', { name: titles[3], exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )

  await expect(page.locator('.ProseMirror')).toBeVisible()
  await page.getByRole('button', { name: 'Page actions' }).click()
  await page.getByRole('menuitem', { name: 'Find and replace' }).click()
  await expect(page.getByTestId('find-bar')).toBeVisible()
})
