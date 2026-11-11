import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('create deal, drag to Won, create invoice from it', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  await page.goto(`${base}/crm`)
  const customer = `Acme ${uniq()}`
  await page.getByRole('button', { name: 'New contact' }).click()
  const cd = page.getByRole('dialog', { name: 'New contact' })
  await cd.getByLabel('Name').fill(customer)
  await cd.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('link', { name: customer })).toBeVisible()

  await page.getByRole('link', { name: 'Deals' }).click()
  const lead = page.getByRole('region', { name: 'Lead stage' })
  await lead.getByRole('button', { name: /New deal/ }).click()
  const title = `Rollout ${uniq()}`
  const dd = page.getByRole('dialog', { name: /New deal/ })
  await dd.getByLabel('Deal title').fill(title)
  await dd.getByLabel('Amount (INR)').fill('50000')
  await dd.getByLabel('Contact').selectOption({ label: customer })
  await dd.getByRole('button', { name: 'Create deal' }).click()

  const card = page.getByLabel(`Deal ${title}`)
  await expect(card).toBeVisible()
  await expect(page.getByTestId('count-lead')).toHaveText('1')

  // dnd-kit's pointer sensor needs real intermediate moves.
  const from = (await card.boundingBox())!
  const to = (await page.getByRole('region', { name: 'Won stage' }).boundingBox())!
  await page.mouse.move(from.x + 10, from.y + from.height - 6)
  const moved = page.waitForResponse((r) => r.url().includes('deal.move_stage') && r.ok())
  await page.mouse.down()
  await page.mouse.move(from.x + 40, from.y + 40, { steps: 5 })
  await page.mouse.move(to.x + 40, to.y + 60, { steps: 15 })
  await page.mouse.up()
  await expect(page.getByTestId('count-won')).toHaveText('1')
  await moved // the board updates optimistically; reload only after the move is stored
  await page.reload()
  await expect(page.getByTestId('count-won')).toHaveText('1')
  await expect(page.getByTestId('count-lead')).toHaveText('0')

  await page.getByRole('region', { name: 'Won stage' }).getByRole('link', { name: title }).click()
  await page.getByRole('button', { name: 'Create invoice' }).click()
  await expect(page).toHaveURL(/\/billing\/[0-9a-f-]+$/)
  await expect(page.getByRole('heading', { name: `Draft invoice for ${customer}` })).toBeVisible()
  await expect(page.getByLabel('Line 1 description')).toHaveValue(title)
  await expect(page.getByLabel('Line 1 unit price')).toHaveValue('50000')
})
