import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('whiteboard shape persists across reload', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('link', { name: 'Whiteboard' }).click()
  await page.getByRole('button', { name: 'New whiteboard' }).click()
  await expect(page).toHaveURL(/\/whiteboard\/[0-9a-f-]+$/)
  const canvas = page.locator('.tl-canvas')
  await expect(canvas).toBeVisible()
  await expect(page.getByText('Live')).toBeVisible()

  await page.mouse.move(400, 400) // focus the canvas without clicking a toolbar
  await page.keyboard.press('r')
  const box = (await canvas.boundingBox())!
  await page.mouse.move(box.x + 200, box.y + 200)
  await page.mouse.down()
  await page.mouse.move(box.x + 360, box.y + 300, { steps: 5 })
  await page.mouse.up()
  const shapes = page.locator('.tl-shape[data-shape-type="geo"]')
  await expect(shapes).toHaveCount(1)

  await page.waitForTimeout(1500) // let the socket flush and the gateway persist
  await page.reload()
  await expect(page.locator('.tl-shape[data-shape-type="geo"]')).toHaveCount(1)
})

test('design: instagram frame and PNG export', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('link', { name: 'Whiteboard' }).click()
  await page.getByRole('button', { name: 'New design' }).click()
  await expect(page.locator('.tl-canvas')).toBeVisible()

  await page.getByLabel('Add frame').selectOption({ label: 'Instagram post 1080x1080' })
  await expect(page.locator('.tl-shape[data-shape-type="frame"]')).toHaveCount(1)

  const dl = page.waitForEvent('download')
  await page.getByRole('button', { name: 'PNG' }).click()
  expect((await dl).suggestedFilename()).toMatch(/\.png$/)
})
