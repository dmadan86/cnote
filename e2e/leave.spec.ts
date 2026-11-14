import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

function nextMonday() {
  const d = new Date(Date.now() + 14 * 86400000)
  while (d.getUTCDay() !== 1 || d.getUTCFullYear() !== new Date(d.getTime() + 86400000).getUTCFullYear())
    d.setUTCDate(d.getUTCDate() + 1)
  const iso = (x: Date) => x.toISOString().slice(0, 10)
  return [iso(d), iso(new Date(d.getTime() + 86400000))]
}

test('staff request leave, the owner approves, balances and holidays are editable', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: ub.email, role: 'member' } })
    ).ok(),
  ).toBe(true)
  const [from, to] = nextMonday()

  await b.goto(`/w/${ws}/people/leave`)
  await expect(b.getByText('12 a year, 0 used, 0 pending').first()).toBeVisible() // seeded, editable defaults
  await b.getByRole('button', { name: 'Request leave' }).click()
  const sheet = b.getByRole('dialog', { name: 'Request leave' })
  await sheet.getByLabel('Leave type').selectOption({ label: 'Casual leave' })
  await sheet.getByLabel('From').fill(from)
  await sheet.getByLabel('To (blank for one day)').fill(to)
  await sheet.getByLabel('Reason').fill('Family function')
  await sheet.getByRole('button', { name: 'Send request' }).click()
  const mine = b.getByRole('list', { name: 'Leave requests' })
  await expect(mine).toContainText('pending')
  await expect(b.getByText('10 left')).toBeVisible() // two days reserved at once
  await expect(b.getByRole('heading', { name: 'Needs your decision' })).toHaveCount(0) // not your own

  // overlapping dates are refused with a reason
  await b.getByRole('button', { name: 'Request leave' }).click()
  const again = b.getByRole('dialog', { name: 'Request leave' })
  await again.getByLabel('From').fill(from)
  await again.getByRole('button', { name: 'Send request' }).click()
  await expect(again.getByRole('alert')).toContainText('already a pending or approved')
  await again.getByRole('button', { name: 'Cancel' }).click()

  // the owner rejects once (reason required), the member re-requests, the owner approves
  await a.goto(`/w/${ws}/people/leave`)
  await a.getByRole('button', { name: `Reject ${ub.name}` }).click()
  await a.getByLabel('Reason for rejecting').fill('Quarter close')
  await a.getByRole('button', { name: 'Confirm rejection' }).click()
  await expect(a.getByRole('list', { name: 'Leave requests' })).toContainText('rejected')
  await b.reload()
  await expect(b.getByText('12 left').first()).toBeVisible()
  await b.getByRole('button', { name: 'Request leave' }).click()
  const third = b.getByRole('dialog', { name: 'Request leave' })
  await third.getByLabel('From').fill(from)
  await third.getByRole('button', { name: 'Send request' }).click()
  await expect(third).toBeHidden()
  await a.reload()
  await a.getByRole('button', { name: `Approve ${ub.name}` }).click()
  await expect(a.getByRole('heading', { name: 'Needs your decision' })).toHaveCount(0)
  await b.reload()
  await expect(b.getByRole('list', { name: 'Leave requests' })).toContainText('approved')

  // the member can cancel; admin sections are owner-only
  await b.getByRole('button', { name: `Cancel leave ${from}` }).click()
  await expect(b.getByRole('list', { name: 'Leave requests' })).toContainText('cancelled')
  await expect(b.getByRole('heading', { name: 'Leave types and holidays' })).toHaveCount(0)

  // holidays: the three national ones are there; the owner adds and removes one
  const holidays = a.getByRole('list', { name: 'Holidays' })
  await expect(holidays).toContainText('Republic Day')
  await expect(holidays).toContainText('Independence Day')
  await expect(holidays).toContainText('Gandhi Jayanti')
  const name = `Founders ${uniq()}`
  const y = new Date().getFullYear()
  await a.getByLabel('Date').fill(`${y}-11-20`)
  await a.getByLabel('Holiday name').fill(name)
  await a.getByRole('button', { name: 'Add holiday' }).click()
  await expect(holidays).toContainText(name)
  await a.getByRole('button', { name: `Remove ${name}` }).click()
  await expect(holidays).not.toContainText(name)
  // leave types: add one with no limit
  const type = `Study ${uniq()}`
  await a.getByLabel('New leave type').fill(type)
  await a.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(a.getByRole('list', { name: 'Leave types' })).toContainText(`${type}No limit`)
})
