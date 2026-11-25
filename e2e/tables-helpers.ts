import { expect, type Page } from '@playwright/test'
import { createSpace, signup } from './helpers'

export const wsOf = (page: Page) => new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string

/** Signs up, creates a space, and opens a new base made from the named template. Returns the base's URL. */
export async function templateBase(page: Page, template = 'Order tracker', name = `Base ${Date.now()}`) {
  await signup(page)
  await createSpace(page)
  await page.goto(`${wsOf(page)}/tables`)
  await page.getByRole('button', { name: 'New base' }).click()
  const d = page.getByRole('dialog', { name: 'New base' })
  await d.getByRole('radio', { name: /From a template/ }).check({ force: true })
  await d.getByRole('radio', { name: new RegExp(template) }).check({ force: true })
  await d.getByLabel('Name').fill(name)
  await d.getByRole('button', { name: 'Create base' }).click()
  await expect(page).toHaveURL(/\/tables\/[0-9a-f-]+/)
  await expect(page.getByRole('heading', { name })).toBeVisible()
  return { url: page.url(), name }
}

export const gridOf = (page: Page, table = 'Orders') => page.getByRole('grid', { name: `${table} records` })

/** The row holding a cell with this exact text. */
export const rowWith = (page: Page, text: string) =>
  gridOf(page)
    .getByRole('row')
    .filter({ has: page.getByRole('gridcell', { name: text, exact: true }) })
