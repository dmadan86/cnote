import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

test('reports: build on tasks, group, save, reopen, add to a new dashboard, export CSV', async ({ page }) => {
  await signup(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  // seed: one project with two tasks
  await page.goto(`${base}/projects`)
  await page.getByRole('button', { name: 'New project' }).click()
  const dlg = page.getByRole('dialog', { name: 'Create project' })
  await dlg.getByLabel('Name').fill(`Proj ${uniq()}`)
  await dlg.getByLabel('Key').fill(`R${uniq().slice(0, 4)}`.toUpperCase())
  await dlg.getByRole('button', { name: 'Create project' }).click()
  await expect(page.getByRole('tab', { name: 'List' })).toBeVisible()
  for (const title of ['Draft the brief', 'Review the brief']) {
    await page.keyboard.press('c')
    const d = page.getByRole('dialog', { name: 'New item' })
    await d.getByLabel('Title').fill(title)
    await d.getByRole('button', { name: 'Create item' }).click()
    await expect(d).toBeHidden()
  }

  // library -> builder from scratch
  await page.getByRole('link', { name: 'Reports', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Reports', level: 1 })).toBeVisible()
  await page.getByRole('link', { name: 'New report' }).click()
  await page.getByLabel('Report on').selectOption({ label: 'Tasks' })
  const preview = page.getByRole('region', { name: 'Preview' })
  await expect(preview.getByText('Draft the brief')).toBeVisible()
  await expect(preview.getByText('Showing only what you can access')).toBeVisible()

  // group by progress: bars appear after choosing Bar
  await page.getByText('Summarise', { exact: true }).click()
  await page.getByLabel('Group by').selectOption({ label: 'Progress' })
  await page.getByRole('button', { name: 'Bar' }).click()
  await expect(preview.getByRole('list', { name: /by Progress/ })).toBeVisible()
  await expect(preview.getByRole('row', { name: /To do/ })).toBeVisible()

  // drill down to the underlying rows
  await preview.getByRole('button', { name: /Show rows for To do/ }).click()
  const drill = page.getByRole('dialog', { name: /Rows for/ })
  await expect(drill.getByText('Review the brief')).toBeVisible()
  await drill.getByRole('button', { name: 'Close' }).click()

  // export CSV
  const dl = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export CSV' }).click()
  expect((await dl).suggestedFilename()).toMatch(/\.csv$/)

  // save, with a new dashboard
  const name = `Tasks by progress ${uniq()}`
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  const save = page.getByRole('dialog', { name: 'Save report' })
  await save.getByLabel('Name').fill(name)
  await save.getByLabel('Add to dashboard').selectOption('new')
  const dash = `Board ${uniq()}`
  await save.getByLabel('Dashboard name').fill(dash)
  await save.getByRole('button', { name: 'Save report' }).click()
  await expect(page).toHaveURL(/\/reports\/[0-9a-f-]+$/)

  // reopen from the library
  await page.goto(`${base}/reports`)
  await page.getByRole('link', { name }).click()
  await expect(page.getByLabel('Report name')).toHaveValue(name)
  await expect(page.getByRole('region', { name: 'Preview' }).getByText('To do').first()).toBeVisible()

  // dashboard shows the tile
  await page.goto(`${base}/reports/dashboards`)
  await page.getByRole('link', { name: dash }).click()
  const tile = page.getByRole('region', { name })
  await expect(tile).toBeVisible()
  await expect(tile.getByText('To do').first()).toBeVisible()
})
