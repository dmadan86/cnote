import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test("a second user cannot open the first user's page", async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  await createSpace(a)
  await a.getByRole('button', { name: 'New page' }).last().click()
  await expect(a).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const url = a.url()

  const b = await (await browser.newContext()).newPage()
  await signup(b)
  await b.goto(url)
  await expect(b.getByRole('heading', { name: 'Not found' })).toBeVisible()
  await expect(b.getByLabel('Page title')).toHaveCount(0)
})
