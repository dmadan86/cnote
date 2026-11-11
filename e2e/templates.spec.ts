import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('save a page as a template, render it from a contact', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  // Author the template page: text plus a field inserted with the picker.
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const editor = page.getByRole('textbox', { name: 'Page content' })
  await expect(editor).toBeVisible()
  const saved = page.waitForResponse(
    (r) => r.url().includes('note.update') && !!r.request().postData()?.includes('content_md'),
  )
  await editor.click()
  await page.keyboard.type('Welcome pack for ')
  await page.getByRole('button', { name: 'Field', exact: true }).click()
  await page.getByLabel('Field source').selectOption('contact')
  await page.getByLabel('Field', { exact: true }).selectOption({ label: 'Contact name' })
  await saved
  await expect(editor).toContainText('Welcome pack for {{contact.name}}')

  const name = `Welcome ${uniq()}`
  await page.getByRole('button', { name: 'Save as template' }).click()
  const save = page.getByRole('dialog', { name: 'Save as template' })
  await save.getByLabel('Template name').fill(name)
  await save.getByLabel('Fills from').selectOption('contact')
  await save.getByRole('button', { name: 'Save template' }).click()
  await expect(save.getByRole('status')).toContainText(name)
  await save.getByRole('button', { name: 'Done' }).click()

  // Render it from a contact.
  await page.goto(`${base}/crm`)
  const customer = `Acme ${uniq()}`
  await page.getByRole('button', { name: 'New contact' }).click()
  const cd = page.getByRole('dialog', { name: 'New contact' })
  await cd.getByLabel('Name').fill(customer)
  await cd.getByRole('button', { name: 'Save contact' }).click()
  await page.getByRole('link', { name: customer }).click()
  await page.getByRole('button', { name: 'Use template' }).click()
  const use = page.getByRole('dialog', { name: 'Use a template' })
  await use.getByLabel('Template').selectOption({ label: name })
  await use.getByRole('button', { name: 'Create' }).click()

  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText(
    `Welcome pack for ${customer}`,
  )
})
