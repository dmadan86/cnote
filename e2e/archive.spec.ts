import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('archive a page with its sub-pages, read-only, then restore with sub-pages', async ({ page }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await page.getByLabel('Page title').fill('Handbook')
  await page.getByLabel('Page title').blur()
  const parent = page.url()
  await page.getByRole('button', { name: 'Sub-page' }).click()
  await expect(page).not.toHaveURL(parent)

  await page.goto(parent)
  await page.getByRole('button', { name: 'Archive', exact: true }).click()
  const dlg = page.getByRole('dialog', { name: 'Archive page' })
  await expect(dlg).toContainText('its 1 sub-page')
  await dlg.getByRole('button', { name: 'Archive', exact: true }).click()
  await expect(page.getByText('Archived. This page is read-only until it is restored')).toBeVisible()
  await expect(page.getByLabel('Page title')).toHaveJSProperty('readOnly', true)
  const res = await page.request.post(`/api/w/${ws}/commands/note.update`, {
    data: { id: parent.split('/n/')[1], title: 'nope' },
  })
  expect(res.status()).toBe(403)

  await page.goto(`/w/${ws}/docs`)
  await page.getByRole('button', { name: 'List' }).click()
  const archived = page.getByRole('region', { name: 'Archived pages' })
  await expect(archived.getByRole('listitem')).toHaveCount(2) // the page and its sub-page
  await page
    .getByRole('listitem')
    .filter({ hasText: 'Handbook' })
    .getByRole('button', { name: 'Restore with sub-pages' })
    .click()
  await expect(archived.getByRole('listitem')).toHaveCount(0)
  await page.goto(parent)
  await expect(page.getByLabel('Page title')).toHaveJSProperty('readOnly', false)
})
