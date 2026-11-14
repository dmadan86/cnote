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

test('Member submits a claim, owner approves and reimburses it, P&L shows it; TDS is withheld on a bill payment', async ({
  browser,
}) => {
  const oc = await browser.newContext()
  const mc = await browser.newContext()
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

    // member: quick-add a claim (draft), then submit it
    await member.goto(`/w/${ws}/billing/expenses`)
    await member.getByRole('button', { name: 'New claim' }).click()
    const form = member.getByRole('dialog', { name: 'New expense claim' })
    await form.getByLabel('Amount paid (INR, GST included)').fill('1800')
    await form.getByLabel('Category').selectOption('travel')
    await form.getByLabel('Paid to (optional)').fill('Auto stand')
    await form.getByRole('button', { name: 'Save draft' }).click()
    const detail = member.getByRole('dialog', { name: /Expense: Travel and transport Rs 1800/ })
    await expect(detail.getByText('Draft', { exact: true })).toBeVisible()
    await detail.getByRole('button', { name: 'Submit claim' }).click()
    await expect(detail.getByText('Waiting for approval')).toBeVisible()
    await member.keyboard.press('Escape')
    await expect(member.getByRole('tab', { name: 'To approve' })).toHaveCount(0) // members have no queues

    // owner: approve, then reimburse
    await owner.goto(`/w/${ws}/billing/expenses`)
    await owner.getByRole('tab', { name: /To approve \(1\)/ }).click()
    await owner.getByRole('button', { name: /Travel and transport at Auto stand/ }).click()
    const od = owner.getByRole('dialog', { name: /Expense: Travel and transport/ })
    await od.getByRole('button', { name: 'Approve' }).click()
    await expect(od.getByText('To reimburse', { exact: true })).toBeVisible()
    await od.getByRole('button', { name: 'Reimburse' }).click()
    await od.getByRole('button', { name: 'Record reimbursement' }).click()
    await expect(od.getByText('Reimbursed', { exact: true })).toBeVisible()
    await owner.keyboard.press('Escape')

    // P&L shows the expense
    await owner.goto(`/w/${ws}/billing/reports`)
    const pl = owner.getByRole('region', { name: 'Profit and loss' })
    await expect(pl.getByText('Travel and transport')).toBeVisible()
    await expect(pl.getByTestId('net-profit')).toContainText('₹1,800.00')

    // TDS: contractor with a default code, bill of Rs 40,000 + 18% GST
    const space = (await call('space.list', {}))[0].id
    const vendor = (
      await call('contact.create', {
        space_id: space,
        name: `Builder ${uniq()}`,
        kind: 'company',
        gstin: gstin('29', 'AABCT1332L'),
        is_vendor: true,
      })
    ).id
    await call('vendor.set_tax', { id: vendor, pan: 'ABCDE1234F', default_tds_code: 'contractor_other' })
    const bill = (
      await call('bill.create_draft', {
        space_id: space,
        vendor_id: vendor,
        vendor_bill_number: `B-${uniq()}`,
        lines: [{ description: 'Slab work', unit_price_minor: 4_000_000 }],
      })
    ).id
    await call('bill.record', { id: bill })
    await owner.goto(`/w/${ws}/billing/bills/${bill}`)
    await owner.getByRole('button', { name: 'Record payment' }).click()
    const pay = owner.getByRole('dialog', { name: 'Record payment' })
    await expect(pay.getByTestId('tds-withheld')).toHaveText('₹800.00')
    await expect(pay.getByTestId('tds-net')).toHaveText('₹46,400.00')
    await pay.getByRole('button', { name: 'Record payment' }).click()
    await expect(owner.getByText('₹800.00 withheld as TDS')).toBeVisible()

    await owner.goto(`/w/${ws}/billing/reports`)
    await expect(owner.getByTestId('tds-owed')).toHaveText('₹800.00')
  } finally {
    await oc.close()
    await mc.close()
  }
})
