import { expect, test } from '@playwright/test'
import { gridOf, rowWith, templateBase, wsOf } from './tables-helpers'

test('record panel: closing waits for the save, refreshes the grid, and stays open when the save fails', async ({
  page,
}) => {
  await templateBase(page)
  const grid = gridOf(page)
  const open = async () => {
    await rowWith(page, 'Steel utensils, 40 sets')
      .getByRole('button', { name: /^Open record/ })
      .click()
    return page.getByRole('dialog', { name: /^Record / })
  }

  // a slow save: Close right after typing must not lose the edit, and the grid shows it
  await page.route('**/commands/record.update', async (route) => {
    await new Promise((r) => setTimeout(r, 800))
    await route.continue()
  })
  let panel = await open()
  await panel.getByLabel('Item', { exact: true }).fill('Copper pots, 12 sets')
  await panel.getByRole('button', { name: 'Close record' }).click()
  await expect(panel).toHaveCount(0)
  await expect(grid.getByRole('gridcell', { name: 'Copper pots, 12 sets' })).toBeVisible()
  await page.unroute('**/commands/record.update')

  // a failing save: the panel stays open and says why
  await page.route('**/commands/record.update', (route) =>
    route.fulfill({
      status: 400,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'invalid', message: 'Item is not valid' }),
    }),
  )
  await rowWith(page, 'Copper pots, 12 sets')
    .getByRole('button', { name: /^Open record/ })
    .click()
  panel = page.getByRole('dialog', { name: /^Record / })
  await panel.getByLabel('Item', { exact: true }).fill('Brass lamps, 3')
  await panel.getByRole('button', { name: 'Close record' }).click()
  await expect(panel).toBeVisible()
  await expect(panel.getByRole('alert').first()).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'Brass lamps, 3' })).toHaveCount(0)
})

test('views: a pending save in one view is not lost when you switch to another view', async ({ page }) => {
  const { url } = await templateBase(page)
  const ws = wsOf(page)
  const baseId = url.match(/\/tables\/([0-9a-f-]+)/)?.[1]
  const base = await (await page.request.post(`/api${ws}/commands/base.get`, { data: { id: baseId } })).json()
  const tableId = base.tables[0].id
  const made = await page.request.post(`/api${ws}/commands/table_view.create`, {
    data: { table_id: tableId, name: 'Second', type: 'grid' },
  })
  expect(made.ok()).toBeTruthy()
  await page.reload()

  const saved = new Set<string>()
  await page.route('**/commands/table_view.update', async (route) => {
    saved.add(JSON.parse(route.request().postData() ?? '{}').id)
    await route.continue()
  })
  const views = page.getByRole('tablist', { name: 'Views' })
  const addSort = async () => {
    await page.getByRole('button', { name: 'Sort', exact: true }).click()
    await page.getByRole('dialog', { name: 'Sort' }).getByRole('button', { name: 'Add sort' }).click()
    await page.keyboard.press('Escape')
  }
  await addSort() // first view: its save is still waiting when we leave
  await views.getByRole('tab', { name: 'Second' }).click()
  await expect(views.getByRole('tab', { name: 'Second' })).toHaveAttribute('aria-selected', 'true')
  await addSort()
  await expect.poll(() => saved.size).toBe(2)
})
