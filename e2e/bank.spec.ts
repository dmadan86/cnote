import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

const CHARS = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'
function gstin(state: string, pan: string) {
  const body = `${state}${pan}1Z`
  const sum = [...body].reduce((s, ch, i) => {
    const v = CHARS.indexOf(ch) * (i % 2 ? 2 : 1)
    return s + Math.floor(v / 36) + (v % 36)
  }, 0)
  return body + CHARS[(36 - (sum % 36)) % 36]
}
const dmy = (offset: number) => {
  const d = new Date(Date.now() + offset * 86400000 + 5.5 * 3600000)
  return `${String(d.getUTCDate()).padStart(2, '0')}/${String(d.getUTCMonth() + 1).padStart(2, '0')}/${d.getUTCFullYear()}`
}
const iso = (offset: number) =>
  new Date(Date.now() + offset * 86400000 + 5.5 * 3600000).toISOString().slice(0, 10)

test('Import an HDFC-style statement, match a payment, record the rest, unmatch; finance gate holds', async ({
  browser,
}) => {
  const oc = await browser.newContext()
  const mc = await browser.newContext({ viewport: { width: 390, height: 800 } })
  const owner = await oc.newPage()
  const member = await mc.newPage()
  try {
    await signup(owner)
    await createSpace(owner)
    const ws = new URL(owner.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
    const call = async (name: string, data: unknown) => {
      const r = await owner.request.post(`/api/w/${ws}/commands/${name}`, { data })
      expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
      return r.json()
    }
    const mu = await signup(member)
    await call('workspace.update', { settings: { gstin: gstin('29', 'ABCDE1234F'), state_code: '29' } })
    await call('member.add', { email: mu.email, role: 'member' })
    const space = (await call('space.list', {}))[0].id
    const customer = `Buyer ${uniq()}`
    await call('contact.create', {
      space_id: space,
      name: customer,
      kind: 'company',
      gstin: gstin('27', 'AAAAA0000A'),
    })
    await call('expense.record_direct', {
      spent_on: iso(-1),
      category: 'office',
      amount_minor: 59000,
      reference: 'POS-7788',
    })

    const csv = [
      'Account Statement',
      '',
      'Date,Narration,Chq./Ref.No.,Value Dt,Withdrawal Amt.,Deposit Amt.,Closing Balance',
      '********,********,********,********,********,********,********',
      `${dmy(-1)},POS OFFICE SUPPLIES POS-7788,x,${dmy(-1)},590.00,,"9,410.00"`,
      `${dmy(0)},UPI-NEW CUSTOMER-77,x,${dmy(0)},,"11,800.00","21,210.00"`,
    ].join('\n')

    await owner.goto(`/w/${ws}/billing/bank`)
    await owner.getByLabel('Create your first bank account').fill('HDFC Current')
    await owner.getByRole('button', { name: 'Add account' }).click()
    await owner
      .getByLabel('Import a statement (CSV)')
      .setInputFiles({ name: 's.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) })
    await expect(owner.getByRole('status').filter({ hasText: '2 lines imported' })).toBeVisible()
    // importing the same file again adds nothing
    await owner
      .getByLabel('Import a statement (CSV)')
      .setInputFiles({ name: 's.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) })
    await expect(
      owner.getByRole('status').filter({ hasText: '0 lines imported, 2 already there' }),
    ).toBeVisible()

    // the debit has a suggestion, ranked with the reference note; confirm it
    const debit = owner.getByRole('listitem').filter({ hasText: 'POS OFFICE SUPPLIES' })
    await expect(debit.getByText('Transaction reference matches')).toBeVisible()
    await debit.getByRole('button', { name: 'Match' }).click()
    await expect(
      owner.getByText('All statement lines are matched').or(owner.getByText('UPI-NEW CUSTOMER-77')),
    ).toBeVisible()
    await expect(owner.getByRole('listitem').filter({ hasText: 'POS OFFICE SUPPLIES' })).toHaveCount(0)

    // the credit has no match: record it as an advance receipt
    const credit = owner.getByRole('listitem').filter({ hasText: 'UPI-NEW CUSTOMER-77' })
    await credit.getByRole('button', { name: 'Record as receipt' }).click()
    const dlg = owner.getByRole('dialog', { name: 'Record as receipt' })
    await dlg.getByLabel('Customer').selectOption({ label: customer })
    await dlg.getByRole('button', { name: 'Record receipt' }).click()
    await expect(owner.getByText('All statement lines are matched')).toBeVisible()

    // matched tab: both lines, unmatch one
    await owner.getByRole('tab', { name: 'Matched', exact: true }).click()
    await expect(
      owner.getByRole('list', { name: 'matched statement lines' }).getByRole('listitem'),
    ).toHaveCount(2)
    await owner
      .getByRole('listitem')
      .filter({ hasText: 'POS OFFICE SUPPLIES' })
      .getByRole('button', { name: 'Unmatch' })
      .click()
    await expect(
      owner.getByRole('list', { name: 'matched statement lines' }).getByRole('listitem'),
    ).toHaveCount(1)
    await owner.getByRole('tab', { name: 'Unmatched', exact: true }).click()
    await expect(owner.getByRole('listitem').filter({ hasText: 'POS OFFICE SUPPLIES' })).toBeVisible()
    await expect(owner.getByText('Statement balance')).toBeVisible()

    // phone width, member with finance access: usable, no sideways scroll
    await member.goto(`/w/${ws}/billing/bank`)
    await expect(member.getByLabel('Create your first bank account')).toBeVisible()
    const wide = await member.evaluate(() =>
      [...document.querySelectorAll('main *')]
        .filter((e) => !e.closest('nav') && e.getBoundingClientRect().right > window.innerWidth + 1)
        .map((e) => `${e.tagName}.${String(e.className).slice(0, 40)}`)
        .slice(0, 5),
    )
    expect(wide, 'bank page content must fit a phone').toEqual([])

    // finance access off: the member's bank page shows the refusal
    const members = await call('member.list', {})
    const row = members.find((m: { email: string }) => m.email === mu.email)
    await call('member.update', { user_id: row.user_id, finance_access: false })
    await member.reload()
    await expect(member.getByText(/finance access is turned off/i).first()).toBeVisible()
  } finally {
    await oc.close()
    await mc.close()
  }
})
