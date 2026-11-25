import { expect, test } from '@playwright/test'
import { templateBase } from './tables-helpers'

test('kanban: drag a card to another column, and move one with the keyboard alternative', async ({
  page,
}) => {
  await templateBase(page)
  await page.getByRole('tab', { name: 'Kanban: By status' }).click()
  const col = (name: string) => page.getByRole('region', { name: `${name} column` })
  const card = (text: string) => page.getByRole('listitem').filter({ hasText: text })
  await expect(card('LED panels, 200 pcs')).toBeVisible()
  await expect(col('New').getByText('LED panels, 200 pcs')).toBeVisible()

  // drag LED panels from New to Packed
  const handle = card('LED panels, 200 pcs').getByRole('button', { name: /^Drag / })
  const from = (await handle.boundingBox()) as { x: number; y: number; width: number; height: number }
  const to = (await col('Packed').boundingBox()) as { x: number; y: number; width: number; height: number }
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2)
  await page.mouse.down()
  await page.mouse.move(from.x + 20, from.y + 20, { steps: 5 })
  await page.mouse.move(to.x + to.width / 2, to.y + 60, { steps: 12 })
  await page.mouse.up()
  await expect(col('Packed').getByText('LED panels, 200 pcs')).toBeVisible()
  await expect(col('New').getByText('LED panels, 200 pcs')).toHaveCount(0)

  // keyboard: the card's "Move to" list
  await card('Pressure cookers, 30')
    .getByLabel(/^Move .* to$/)
    .selectOption({ label: 'Move to Dispatched' })
  await expect(page.getByRole('status').filter({ hasText: 'Moved to Dispatched' }).first()).toBeAttached()
  await expect(col('Dispatched').getByText('Pressure cookers, 30')).toBeVisible()

  // both moves were saved
  await page.reload()
  await page.getByRole('tab', { name: 'Kanban: By status' }).click()
  await expect(col('Packed').getByText('LED panels, 200 pcs')).toBeVisible()
  await expect(col('Dispatched').getByText('Pressure cookers, 30')).toBeVisible()
})
