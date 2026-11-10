import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('space -> page -> content persists -> search -> comment', async ({ page }) => {
  await signup(page)
  await createSpace(page)

  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)

  const word = `zebra${uniq()}`
  const titleSaved = page.waitForResponse(
    (r) => r.url().includes('note.update') && !!r.request().postData()?.includes('"title"'),
  )
  await page.getByLabel('Page title').fill(`Plan ${word}`)
  await page.getByLabel('Page title').press('Enter')
  await titleSaved

  const editor = page.getByRole('textbox', { name: 'Page content' })
  await expect(editor).toBeVisible()
  const saved = page.waitForResponse(
    (r) => r.url().includes('note.update') && !!r.request().postData()?.includes('content_md'),
  )
  await editor.click()
  await page.keyboard.type(`Hello ${word} body`)
  await saved

  await page.reload()
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText(`Hello ${word} body`)

  await page.keyboard.press('ControlOrMeta+k')
  await page.getByRole('combobox').fill(word)
  await expect(page.getByRole('option', { name: new RegExp(`Plan ${word}`) })).toBeVisible()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)

  await page.getByRole('tab', { name: 'comments' }).click()
  await page.getByLabel('Add a comment').fill('Looks good to me')
  await page.getByRole('button', { name: 'Comment', exact: true }).click()
  await expect(page.getByText('Looks good to me')).toBeVisible()
})
