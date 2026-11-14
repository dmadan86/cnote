import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('owner finds who did what, exports it, and erases a contact', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const ws = new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1]
  const call = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
    return r.json()
  }
  const spaces = await call('space.list', {})
  const person = `Meera ${uniq()}`
  const contact = await call('contact.create', {
    space_id: spaces[0].id,
    name: person,
    kind: 'person',
    email: 'meera@example.test',
    phone: '9876543210',
  })

  // audit log: filter to contact events, see them, export
  await page.goto(`/w/${ws}/settings?tab=Audit%20log`)
  await page.getByLabel(/Action starts with/).fill('contact.')
  await page.getByRole('button', { name: 'Apply filters' }).click()
  const events = page.getByRole('list', { name: 'Audit events' })
  await expect(events.getByText('contact.created')).toBeVisible()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export filtered CSV' }).click()
  expect((await download).suggestedFilename()).toBe('audit-log.csv')

  // privacy: export, then erase with typed confirmation
  await page.goto(`/w/${ws}/settings?tab=Privacy`)
  await page.getByLabel('Find a contact').fill('Meera')
  await page.getByRole('button', { name: new RegExp(person) }).click()
  const exp = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export their data (JSON)' }).click()
  expect((await exp).suggestedFilename()).toContain('-data.json')
  await page.getByRole('button', { name: 'Erase...' }).click()
  const dialog = page.getByRole('dialog', { name: /Erase/ })
  await expect(dialog.getByText('Invoices, receipts, bills and the ledger stay')).toBeVisible()
  const go = dialog.getByRole('button', { name: 'Anonymise contact' })
  await expect(go).toBeDisabled()
  await dialog.getByLabel(/Reason/).fill('asked to be forgotten')
  await dialog.getByLabel(/Type the name/).fill(person)
  await go.click()
  await expect(page.getByRole('status').filter({ hasText: 'Contact anonymised' })).toBeVisible()
  await expect(page.getByRole('list').filter({ hasText: 'asked to be forgotten' })).toBeVisible()

  const gone = await call('contact.get', { id: contact.id })
  expect(gone.email).toBeNull()
  expect(gone.name).not.toContain('Meera')

  // the erase is in the audit log
  await page.goto(`/w/${ws}/settings?tab=Audit%20log`)
  await page.getByLabel(/Action starts with/).fill('data.')
  await page.getByRole('button', { name: 'Apply filters' }).click()
  await expect(events.getByText('data.erased')).toBeVisible()
})
