import { expect, test } from '@playwright/test'
import { newUser, signup } from './helpers'

test('set a salary structure, run payroll, approve, finalise, pay, payslip and bank file', async ({
  browser,
}) => {
  const a = await (await browser.newContext({ acceptDownloads: true })).newPage()
  const owner = await signup(a)
  const ws = a.url().split('/w/')[1]

  // a member without admin rights does not get the payroll tab, only their own payslips
  const b = await (await browser.newContext()).newPage()
  const member = await signup(b, newUser())
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/member.add`, {
        data: { email: member.email, role: 'member' },
      })
    ).ok(),
  ).toBe(true)
  await b.goto(`/w/${ws}/people/payslips`)
  await expect(b.getByRole('heading', { name: 'My payslips' })).toBeVisible()
  await expect(b.getByRole('link', { name: 'Payroll', exact: true })).toHaveCount(0)
  await b.goto(`/w/${ws}/people/payroll`)
  await expect(b.getByText(/for owners and admins/)).toBeVisible()

  // owner: salary structure for themselves
  await a.goto(`/w/${ws}/people/payroll`)
  await a.getByRole('button', { name: `Set salary structure of ${owner.name}` }).click()
  const form = a.getByRole('dialog', { name: /Salary structure/ })
  await form.getByLabel('Basic (Rs a month)').fill('30000')
  await form.getByLabel('HRA (Rs a month)').fill('12000')
  await form.getByLabel('Special allowance (Rs a month)').fill('8000')
  await expect(form).toContainText('₹50,000.00')
  await form.getByLabel('State for professional tax').selectOption('29')
  await form.getByLabel('PAN').fill('ABCDE1234F')
  await form.getByLabel('Account number').fill('123456789012')
  await form.getByLabel('IFSC').fill('HDFC0001234')
  await form.getByRole('button', { name: 'Save structure' }).click()
  await expect(a.getByRole('list', { name: 'Salary structures' })).toContainText('bank ********9012')

  // draft run for last month (the default)
  await a.getByRole('button', { name: 'Create draft payroll' }).click()
  await expect(a.getByRole('heading', { name: /^Payroll for / })).toBeVisible()
  await expect(
    a.getByRole('list', { name: 'Progress' }).or(a.getByRole('list', { name: 'Progress' })),
  ).toBeVisible()
  const table = a.getByRole('table', { name: 'Pay of each employee for the month' })
  await expect(table).toContainText(owner.name)
  await expect(table).toContainText('₹1,800.00') // EPF on the ₹15,000 ceiling

  // one-off bonus with a reason
  await a
    .getByRole('button', { name: `Adjust pay of ${owner.name}` })
    .first()
    .click()
  const adj = a.getByRole('dialog', { name: /Adjust pay/ })
  await adj.getByLabel('Amount (Rs)').fill('1000')
  await adj.getByLabel('Reason (required)').fill('Festival bonus')
  await adj.getByRole('button', { name: 'Add adjustment' }).click()
  await expect(table).toContainText('Festival bonus')

  // approve, finalise, record payment
  await a.getByRole('button', { name: 'Approve run' }).click()
  await a.getByRole('button', { name: 'Finalise…' }).click()
  await a
    .getByRole('dialog', { name: /Finalise/ })
    .getByRole('button', { name: 'Finalise and post' })
    .click()
  await expect(a.getByText('finalised', { exact: true })).toBeVisible()
  const csv = a.waitForEvent('download')
  await a.getByRole('button', { name: 'Download bank payout CSV' }).click()
  expect((await csv).suggestedFilename()).toMatch(/^salary-payout-\d{4}-\d{2}\.csv$/)
  await a.getByRole('button', { name: 'Record payment…' }).click()
  await a
    .getByRole('dialog', { name: 'Record salary payment' })
    .getByRole('button', { name: 'Record payment' })
    .click()
  await expect(a.getByText('paid', { exact: true })).toBeVisible()
  const pf = a.waitForEvent('download')
  await a.getByRole('button', { name: 'PF (ECR-style) CSV' }).click()
  expect((await pf).suggestedFilename()).toMatch(/^pf-\d{4}-\d{2}\.csv$/)

  // re-running the finalised month is refused
  await a.goto(`/w/${ws}/people/payroll`)
  await a.getByRole('button', { name: 'Create draft payroll' }).click()
  await expect(a.getByText(/already paid/)).toBeVisible()

  // the owner's own payslip, masked, printable
  await a.goto(`/w/${ws}/people/payslips`)
  await a.getByRole('list', { name: 'My payslips' }).getByRole('link').first().click()
  await expect(a.getByRole('heading', { name: /^Payslip, / })).toBeVisible()
  await expect(a.getByRole('article')).toContainText('******234F')
  await expect(a.getByRole('article')).toContainText('Festival bonus')
  await expect(a.getByRole('button', { name: 'Print or save as PDF' })).toBeVisible()

  // the member has no payslip and cannot open the owner's
  await b.goto(`/w/${ws}/people/payslips`)
  await expect(b.getByText('No payslips yet')).toBeVisible()
})
