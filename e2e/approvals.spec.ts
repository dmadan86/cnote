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

test('Member sends a large quote, owner approves it, then a second one is rejected', async ({ browser }) => {
  const oc = await browser.newContext()
  const mc = await browser.newContext()
  const owner = await oc.newPage()
  const member = await mc.newPage()
  try {
    await signup(owner)
    await createSpace(owner)
    const ws = new URL(owner.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
    const call = async (p: typeof owner, name: string, data: unknown) => {
      const r = await p.request.post(`/api/w/${ws}/commands/${name}`, { data })
      expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
      return r.json()
    }
    const mu = await signup(member)
    await call(owner, 'workspace.update', {
      settings: { gstin: gstin('29', 'ABCDE1234F'), state_code: '29' },
    })
    await call(owner, 'member.add', { email: mu.email, role: 'member' })
    const space = (await call(owner, 'space.list', {}))[0].id
    const contact = (
      await call(owner, 'contact.create', {
        space_id: space,
        name: 'Big Buyer',
        kind: 'company',
        gstin: gstin('27', 'AAAAA0000A'),
      })
    ).id
    const quote = async (price: number) =>
      (
        await call(member, 'quote.create_draft', {
          space_id: space,
          contact_id: contact,
          lines: [{ description: 'Website design', unit_price_minor: price }],
        })
      ).id as string

    // owner: Settings > Approvals, quotes from Rs 5,000 need approval
    await owner.goto(`/w/${ws}/settings?tab=Approvals`)
    await owner.getByLabel('Require approval to send a quote').check()
    await owner.getByLabel('From amount').first().fill('5000')
    await owner.getByRole('button', { name: 'Save' }).first().click()
    await expect(owner.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()

    // member: the big quote is held, the page says who decides
    const big = await quote(1_000_000)
    await member.goto(`/w/${ws}/billing/quotes/${big}`)
    await member.getByRole('button', { name: 'Send quote' }).click()
    await member
      .getByRole('dialog', { name: 'Send quotation' })
      .getByRole('button', { name: 'Send quote' })
      .click()
    await expect(member.getByRole('status').filter({ hasText: 'waiting for approval' })).toBeVisible()
    await expect(member.getByText('Draft quotation')).toBeVisible()

    // owner: home tile and inbox, approve
    await owner.goto(`/w/${ws}`)
    await expect(owner.getByText('1 to approve')).toBeVisible()
    await owner.goto(`/w/${ws}/approvals`)
    await expect(owner.getByRole('tab', { name: /Waiting for me \(1\)/ })).toBeVisible()
    await owner.getByRole('button', { name: /^Approve send quote/ }).click()
    await expect(owner.getByText('Nothing needs your approval.')).toBeVisible()

    // member: the quote went out, and the bell says so
    await member.goto(`/w/${ws}/billing/quotes/${big}`)
    await expect(member.getByRole('heading', { name: /^Quotation QT\// })).toBeVisible()
    await member.getByRole('button', { name: 'Notifications' }).click()
    await expect(member.getByText(/Approved: /)).toBeVisible()

    // a second request is rejected with a reason and stays a draft
    const small = await quote(2_000_000)
    await member.goto(`/w/${ws}/billing/quotes/${small}`)
    await member.getByRole('button', { name: 'Send quote' }).click()
    await member
      .getByRole('dialog', { name: 'Send quotation' })
      .getByRole('button', { name: 'Send quote' })
      .click()
    await expect(member.getByRole('status').filter({ hasText: 'waiting for approval' })).toBeVisible()
    await owner.goto(`/w/${ws}/approvals`)
    await owner.getByRole('button', { name: 'Reject' }).click()
    await owner.getByLabel(/Reason for rejecting/).fill('Rate too low')
    await owner.getByRole('button', { name: 'Submit rejection' }).click()
    await expect(owner.getByText('Nothing needs your approval.')).toBeVisible()
    await member.reload()
    await expect(member.getByRole('status').filter({ hasText: 'rejected: Rate too low' })).toBeVisible()
  } finally {
    await oc.close()
    await mc.close()
  }
})
