import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

const CHARS = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'
function gstin(state: string, pan: string) {
  const body = `${state}${pan}1Z`
  const sum = [...body].reduce((s, ch, i) => {
    const v = CHARS.indexOf(ch) * (i % 2 ? 2 : 1)
    return s + Math.floor(v / 36) + (v % 36)
  }, 0)
  return body + CHARS[(36 - (sum % 36)) % 36]
}

test('Branch with its own GSTIN drives place of supply, and a two-step chain needs both approvers', async ({
  browser,
}) => {
  const oc = await browser.newContext()
  const ac = await browser.newContext()
  const rc = await browser.newContext()
  const owner = await oc.newPage()
  const approver = await ac.newPage()
  const requester = await rc.newPage()
  try {
    await signup(owner)
    await createSpace(owner)
    const ws = new URL(owner.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
    const call = async (p: typeof owner, name: string, data: unknown) => {
      const r = await p.request.post(`/api/w/${ws}/commands/${name}`, { data })
      expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
      return r.json()
    }
    const au = await signup(approver)
    const ru = await signup(requester)
    await call(owner, 'workspace.update', {
      settings: { gstin: gstin('29', 'ABCDE1234F'), state_code: '29' },
    })
    await call(owner, 'member.add', { email: au.email, role: 'member' })
    await call(owner, 'member.add', { email: ru.email, role: 'member' })
    const space = (await call(owner, 'space.list', {}))[0].id
    const contact = (
      await call(owner, 'contact.create', {
        space_id: space,
        name: 'Karnataka Buyer',
        kind: 'company',
        gstin: gstin('29', 'AAAAA0000A'),
      })
    ).id

    // Branches section on the People page
    await owner.goto(`/w/${ws}/people`)
    const branches = owner.getByRole('region', { name: 'Branches' })
    await branches.getByLabel('New branch').fill('Pune')
    await branches.getByLabel('GSTIN (optional)').fill(gstin('27', 'ABCDE1234F'))
    await branches.getByRole('button', { name: 'Add branch' }).click()
    await expect(branches.getByLabel('Name of Pune')).toBeVisible()

    // an invoice raised from the branch is inter-state although buyer and head office are both in Karnataka
    await owner.goto(`/w/${ws}/billing`)
    await owner.getByRole('button', { name: 'New invoice' }).click()
    await owner.getByRole('dialog').getByLabel('Branch').selectOption({ index: 1 })
    await owner.getByLabel('Customer').selectOption({ label: 'Karnataka Buyer' })
    await owner.getByRole('button', { name: 'Create draft' }).click()
    await expect(owner).toHaveURL(new RegExp(`/w/${ws}/billing/[0-9a-f-]+$`))
    const id = owner.url().split('/').pop() as string
    await call(owner, 'invoice.update_draft', {
      id,
      lines: [{ description: 'Work', hsn_sac: '998314', unit_price_minor: 100_000, gst_rate_bp: 1800 }],
    })
    const issued = await call(owner, 'invoice.issue', { id })
    expect(issued.supplier_state).toBe('27')
    expect(issued.cgst_minor).toBe(0)

    // the invoice list can be narrowed by branch
    await owner.goto(`/w/${ws}/billing`)
    await owner.getByLabel('Branch').first().selectOption({ index: 1 })
    await expect(owner.getByRole('table')).toBeVisible()

    // chain: the named approver first, then the owner
    const people = (await call(owner, 'member.list', {})) as { user_id: string; email: string }[]
    const approverId = people.find((p) => p.email === au.email)?.user_id as string
    await owner.goto(`/w/${ws}/settings?tab=Approvals`)
    await owner.getByLabel('Require approval to send a quote').check()
    await owner.getByLabel('Step 1 approver 1').selectOption(approverId)
    await owner.getByRole('button', { name: 'Add step' }).click()
    await expect(owner.getByLabel('Step 2 approver 1')).toHaveValue('owner')
    await owner.getByRole('button', { name: 'Save' }).first().click()
    await expect(owner.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()

    const q = (
      await call(requester, 'quote.create_draft', {
        space_id: space,
        contact_id: contact,
        lines: [{ description: 'Design', unit_price_minor: 500_000 }],
      })
    ).id
    const held = await call(requester, 'quote.send', { id: q })
    expect(held.approval_required).toBe(true)

    await approver.goto(`/w/${ws}/approvals`)
    await expect(approver.getByText('Step 1 of 2')).toBeVisible()
    await approver.getByRole('button', { name: /^Approve send quote/ }).click()
    await expect(approver.getByText('Nothing needs your approval.')).toBeVisible()
    expect((await call(requester, 'quote.get', { id: q })).status).toBe('draft')

    await owner.goto(`/w/${ws}/approvals`)
    await expect(owner.getByText('Step 2 of 2')).toBeVisible()
    await owner.getByRole('button', { name: /^Approve send quote/ }).click()
    await expect(owner.getByText('Nothing needs your approval.')).toBeVisible()
    expect((await call(requester, 'quote.get', { id: q })).status).toBe('sent')
  } finally {
    await oc.close()
    await ac.close()
    await rc.close()
  }
})
