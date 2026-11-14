import { randomBytes } from 'node:crypto'
import { expect, type Page } from '@playwright/test'

export const uniq = () => randomBytes(4).toString('hex')

/** Fresh credentials per run: unique email and a random password, never literals. */
export function newUser() {
  const id = uniq()
  return {
    name: `Tester ${id}`,
    email: `e2e-${Date.now()}-${id}@example.test`,
    password: randomBytes(12).toString('hex'),
  }
}

/** The first-run business-type chooser is skipped unless a test wants it. */
export async function signup(page: Page, u = newUser(), { skipPicker = true } = {}) {
  await page.goto('/signup')
  await page.getByLabel('Your name').fill(u.name)
  await page.getByLabel('Email').fill(u.email)
  await page.getByLabel('Password').fill(u.password)
  await page.getByLabel('Workspace name').fill(`WS ${uniq()}`)
  await page.getByRole('button', { name: 'Sign up' }).click()
  await expect(page).toHaveURL(/\/w\/[0-9a-f-]+$/)
  if (skipPicker) {
    await page
      .getByRole('dialog', { name: /What kind of business/ })
      .getByRole('button', { name: 'Skip' })
      .click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
  }
  return u
}

export async function createSpace(page: Page, name = `Space ${uniq()}`) {
  await page.getByRole('button', { name: 'New space' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create space' })
  await dialog.getByLabel('Name').fill(name)
  await dialog.getByLabel('Key').fill(`E${uniq().slice(0, 4)}`)
  await dialog.getByRole('button', { name: 'Create space' }).click()
  await expect(page.getByRole('heading', { name })).toBeVisible()
  return name
}
