import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

test('projects: space -> items -> CNQL -> board drag -> detail -> sprint', async ({ page }) => {
  await signup(page)
  await page.getByRole('link', { name: 'Projects' }).click()
  await page.getByRole('button', { name: 'New project' }).click()
  const dlg = page.getByRole('dialog', { name: 'Create project' })
  const name = `Proj ${uniq()}`
  await dlg.getByLabel('Name').fill(name)
  await dlg.getByLabel('Key').fill(`P${uniq().slice(0, 4)}`)
  await dlg.getByRole('button', { name: 'Create project' }).click()
  await expect(page.getByRole('heading', { name })).toBeVisible()

  // The `c` shortcut is registered once the list view has mounted.
  await expect(page.getByRole('tab', { name: 'List' })).toBeVisible()
  const titles = ['Alpha task', 'Beta task', 'Gamma task']
  for (const [i, t] of titles.entries()) {
    // `c` right after the previous create used to be swallowed by the old dialog's late close event.
    await page.keyboard.press('c')
    const d = page.getByRole('dialog', { name: 'New item' })
    await d.getByLabel('Title').fill(t)
    await d.getByLabel('Priority').selectOption(i === 0 ? 'urgent' : 'low')
    await d.getByRole('button', { name: 'Create item' }).click()
    await expect(d).toBeHidden()
    await expect(page.getByRole('link', { name: t })).toBeVisible()
  }

  // CNQL narrows the list; a bad query reports its position.
  await page.getByRole('tab', { name: 'CNQL' }).click()
  await page.getByLabel('CNQL query').fill('priority = urgent')
  await page.getByRole('button', { name: 'Run' }).click()
  await expect(page.getByRole('link', { name: 'Alpha task' })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Beta task' })).toHaveCount(0)
  await page.getByLabel('CNQL query').fill('priority = ((')
  await page.getByRole('button', { name: 'Run' }).click()
  await expect(page.locator('main [role=alert]')).toContainText('at column')
  await page.getByLabel('CNQL query').fill('')
  await page.getByRole('button', { name: 'Run' }).click()
  await expect(page.getByRole('link', { name: 'Beta task' })).toBeVisible()

  // Board: drag a card to In Progress.
  await page.getByRole('tab', { name: 'Board' }).click()
  const card = page.getByRole('button', { name: /Beta task/ })
  const target = page.getByRole('region', { name: 'In Progress column' })
  const [c, t] = [await card.boundingBox(), await target.boundingBox()]
  if (!c || !t) throw new Error('no boxes')
  await page.mouse.move(c.x + 20, c.y + 10)
  await page.mouse.down()
  await page.mouse.move(c.x + 30, c.y + 20, { steps: 3 })
  await page.mouse.move(t.x + t.width / 2, t.y + 40, { steps: 10 })
  await page.mouse.up()
  await expect(target.getByText('Beta task')).toBeVisible()

  // Item detail: priority + comment.
  await page.getByRole('tab', { name: 'List' }).click()
  await page.getByRole('link', { name: 'Gamma task' }).click()
  await expect(page).toHaveURL(/\/projects\/P[0-9A-F]+-\d+$/)
  const prioritySaved = page.waitForResponse((r) => r.url().includes('item.update') && r.ok())
  await page.getByLabel('Priority').selectOption('high')
  await expect(page.getByLabel('Priority')).toHaveValue('high')
  await page.getByLabel('Add a comment').fill('Ship it')
  await page.getByRole('button', { name: 'Comment', exact: true }).click()
  await expect(page.getByText('Ship it')).toBeVisible()
  await prioritySaved
  await page.reload()
  await expect(page.getByLabel('Priority')).toHaveValue('high')

  // Sprint: create, add an item, start.
  await page.goBack()
  await page.getByRole('tab', { name: 'Sprints' }).click()
  await page.getByLabel('Sprint name').fill('Sprint 1')
  await page.getByRole('button', { name: 'Create sprint' }).click()
  await expect(page.getByRole('region', { name: 'Sprint 1' })).toBeVisible()
  await page.getByLabel(/Sprint for .*-1$/).selectOption({ label: 'Sprint 1' })
  const sprint = page.getByRole('region', { name: 'Sprint 1' })
  await expect(sprint.getByRole('link', { name: 'Alpha task' })).toBeVisible()
  await page.getByRole('button', { name: 'Start sprint' }).click()
  await expect(sprint.getByText('active')).toBeVisible()
  await expect(page.getByRole('img', { name: /Burndown/ })).toBeVisible()
})
