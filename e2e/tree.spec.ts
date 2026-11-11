import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

test('sidebar tree folds pages and spaces, and remembers it', async ({ page }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const cmd = async (name: string, data: unknown) =>
    (await page.request.post(`/api/w/${ws}/commands/${name}`, { data })).json()
  const [space] = await cmd('space.list', {})
  const parent = await cmd('note.create', { space_id: space.id, title: `Parent ${uniq()}` })
  const child = `Child ${uniq()}`
  await cmd('note.create', { space_id: space.id, title: child, parent_id: parent.id })
  await page.reload()

  const tree = page.getByRole('complementary')
  const childLink = tree.getByRole('link', { name: child })
  await expect(tree.getByRole('link', { name: parent.title })).toBeVisible()
  await expect(childLink).toHaveCount(0) // pages start folded

  await tree.getByRole('button', { name: `Expand ${parent.title}` }).click()
  await expect(childLink).toBeVisible()
  await page.reload()
  await expect(childLink).toBeVisible() // remembered

  await tree.getByRole('button', { name: `Collapse ${space.name}` }).click()
  await expect(tree.getByRole('link', { name: parent.title })).toHaveCount(0)
})
