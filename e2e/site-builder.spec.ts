import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

test('Website: create from a template, edit by chat, undo', async ({ page }) => {
  await signup(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
  const ws = base.split('/')[2]
  const name = `Pune Dental ${uniq()}`
  const set = await page.request.post(`/api/w/${ws}/commands/company.update`, {
    data: { trade_name: name, phone: '+919876543210' },
  })
  expect(set.ok()).toBeTruthy()

  await page.goto(`${base}/site`)
  await expect(page.getByRole('heading', { name: 'Create your website' })).toBeVisible()
  await page.getByRole('button', { name: 'Use this' }).first().click()

  // The template is rendered with the business name inside the preview frame.
  const frame = page.frameLocator('iframe[title="Site preview"]')
  await expect(frame.getByRole('heading', { level: 1 })).toContainText(name)
  await expect(frame.getByText('+919876543210').first()).toBeVisible()

  // Selecting a block shows the "Editing" chip.
  await frame.getByRole('button', { name: 'Select Hero' }).click()
  await expect(page.getByText('Editing: Hero')).toBeVisible()

  const sites = await (await page.request.post(`/api/w/${ws}/commands/site.list`, { data: {} })).json()
  const got = await (
    await page.request.post(`/api/w/${ws}/commands/site.get`, { data: { id: sites[0].id } })
  ).json()
  const hero = got.data.content.find((b: { type: string }) => b.type === 'Hero').props.id as string

  // A chat edit through the Local provider's /tool shortcut.
  await page
    .getByLabel('Ask for a change')
    .fill(`/tool update_block_props {"id":"${hero}","props":{"title":"Hello Pune"}}`)
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(frame.getByRole('heading', { level: 1 })).toHaveText('Hello Pune', { timeout: 60_000 })
  const card = page.getByRole('button', { name: 'Undo' }).filter({ hasText: 'Undo' }).last()
  await expect(card).toBeVisible()

  // Undo restores the old title; the card offers Redo.
  await card.click()
  await expect(frame.getByRole('heading', { level: 1 })).toContainText(name)
  await expect(page.getByRole('button', { name: 'Redo' }).filter({ hasText: 'Redo' })).toBeVisible()

  // The mobile toggle narrows the frame to 390 px.
  await page.getByRole('button', { name: 'Mobile' }).click()
  await expect
    .poll(async () => (await page.locator('iframe[title="Site preview"]').boundingBox())?.width)
    .toBe(390)
})
