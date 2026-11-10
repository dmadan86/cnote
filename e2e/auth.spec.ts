import { expect, test } from '@playwright/test'
import { newUser, signup } from './helpers'

test('signup creates a workspace, logout, login', async ({ page }) => {
  const u = await signup(page, newUser())
  await expect(page.getByRole('heading', { name: /Welcome back/ })).toBeVisible()

  await page.getByRole('button', { name: 'User menu' }).click()
  await page.getByRole('menuitem', { name: 'Log out' }).click()
  await expect(page).toHaveURL(/\/login$/)

  await page.getByLabel('Email').fill(u.email)
  await page.getByLabel('Password').fill(u.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page).toHaveURL(/\/w\/[0-9a-f-]+$/)
})
