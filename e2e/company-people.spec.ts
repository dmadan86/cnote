import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// A GSTIN with a valid check digit (state 29, PAN ABCDE1234F).
const GSTIN = '29ABCDE1234F1ZW'

test('owner fills company details, organises people, hides finance and deactivates someone who left', async ({
  browser,
}) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  const added = await a.request.post(`/api/w/${ws}/commands/member.add`, {
    data: { email: ub.email, role: 'member' },
  })
  expect(added.ok()).toBe(true)

  // Company profile: a wrong PAN is explained, the preview follows typing, the values persist.
  await a.goto(`/w/${ws}/settings?tab=Company`)
  const form = a.getByRole('form', { name: 'Company profile' })
  await form.getByLabel('Legal name').fill('Acme Works Pvt Ltd')
  await form.getByLabel('PAN').fill('abcde123')
  await form.getByRole('button', { name: 'Save company details' }).click()
  await expect(form.getByText(/PAN looks like/)).toBeVisible()
  await form.getByLabel('PAN').fill('abcde1234f')
  await form.getByLabel('GSTIN').fill(GSTIN.toLowerCase())
  await expect(form.getByText('State from GSTIN: 29 Karnataka')).toBeVisible()
  await form.getByLabel('Address').fill('12 MG Road\nBengaluru')
  await form.getByLabel('Phone').fill('98765 43210')
  const preview = a.getByRole('complementary', { name: 'Invoice header preview' })
  await expect(preview).toContainText('Acme Works Pvt Ltd')
  await expect(preview).toContainText(`GSTIN ${GSTIN}`)
  await form.getByRole('button', { name: 'Save company details' }).click()
  await expect(form.getByRole('status')).toHaveText('Saved')
  await a.reload()
  await expect(a.getByLabel('Legal name')).toHaveValue('Acme Works Pvt Ltd')
  await expect(a.getByRole('textbox', { name: 'PAN' })).toHaveValue('ABCDE1234F')

  // People: a department, then the member's profile and finance switch.
  await a.goto(`/w/${ws}/people`)
  const dept = `Site ${uniq()}`
  await a.getByLabel('New department').fill(dept)
  await a.getByRole('button', { name: 'Add department' }).click()
  await expect(a.getByLabel(`Name of ${dept}`)).toBeVisible()
  await a.getByRole('button', { name: `Edit ${ub.name}` }).click()
  const edit = a.getByRole('dialog', { name: `Edit ${ub.name}` })
  await edit.getByLabel('Department').selectOption({ label: dept })
  await edit.getByLabel('Reports to').selectOption({ index: 1 })
  await edit.getByLabel('Designation').fill('Site engineer')
  await edit.getByLabel('Can see finance').uncheck()
  await edit.getByRole('button', { name: 'Save' }).click()
  const row = a.getByRole('list', { name: 'People' }).getByRole('listitem').filter({ hasText: ub.name })
  await expect(row).toContainText(`Site engineer · ${dept}`)
  await expect(row).toContainText('No finance')
  await a.getByLabel('Department', { exact: true }).selectOption({ label: dept })
  await expect(a.getByRole('list', { name: 'People' }).getByRole('listitem')).toHaveCount(1)

  // The site engineer cannot see invoices; everything else works.
  const money = await b.request.post(`/api/w/${ws}/commands/invoice.list`, { data: {} })
  expect(money.status()).toBe(403)
  expect((await b.request.post(`/api/w/${ws}/commands/people.list`, { data: {} })).ok()).toBe(true)

  // They leave: deactivate with a confirmation; access stops; they can come back.
  await a.getByRole('button', { name: `Edit ${ub.name}` }).click()
  await a
    .getByRole('dialog', { name: `Edit ${ub.name}` })
    .getByRole('button', { name: 'Deactivate' })
    .click()
  await a.getByRole('button', { name: `Deactivate ${ub.name}` }).click()
  await expect(a.getByRole('list', { name: 'People' })).toContainText('No one matches.')
  await a.getByLabel('Include inactive').check()
  await expect(a.getByRole('list', { name: 'People' })).toContainText('Inactive')
  expect((await b.request.post(`/api/w/${ws}/commands/people.list`, { data: {} })).status()).toBe(403)
  await a.getByRole('button', { name: `Edit ${ub.name}` }).click()
  await a
    .getByRole('dialog', { name: `Edit ${ub.name}` })
    .getByRole('button', { name: 'Reactivate' })
    .click()
  await expect
    .poll(async () => (await b.request.post(`/api/w/${ws}/commands/people.list`, { data: {} })).status())
    .toBe(200)
})
