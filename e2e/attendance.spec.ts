import { expect, test } from '@playwright/test'
import { signup } from './helpers'

test('staff check in and out on a phone; the owner sees people present and marks the rest', async ({
  browser,
}) => {
  const a = await (await browser.newContext()).newPage()
  const ua = await signup(a)
  const ws = a.url().split('/w/')[1]
  const b = await (await browser.newContext({ viewport: { width: 375, height: 700 } })).newPage()
  const ub = await signup(b)
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: ub.email, role: 'member' } })
    ).ok(),
  ).toBe(true)

  // the member's workspace home: one big button
  await b.goto(`/w/${ws}`)
  const card = b.getByRole('region', { name: 'Your attendance' })
  await expect(card).toContainText('You have not checked in yet')
  await expect(card.getByRole('button', { name: 'Check in' })).toBeInViewport({ ratio: 1 }) // reachable on a phone
  await card.getByRole('button', { name: 'Check in' }).click()
  await expect(card).toContainText(/Checked in at/)
  await card.getByRole('button', { name: 'Check out' }).click()
  await expect(card).toContainText(/out at/)

  // the owner: the home tile counts the member, the board lists them and can mark the owner
  await a.goto(`/w/${ws}`)
  await expect(a.getByRole('link', { name: /People present: 1 of/ })).toBeVisible()
  await a.goto(`/w/${ws}/people/attendance`)
  await expect(a.getByRole('list', { name: 'Attendance today' })).toContainText(ub.name)
  await a.getByRole('button', { name: /^Present/ }).click()
  await expect(a.getByRole('list', { name: 'Attendance today' }).getByRole('listitem')).toHaveCount(1)
  await a.getByRole('button', { name: /^Everyone/ }).click()
  await a.getByLabel(`Mark ${ua.name}`).selectOption('present')
  await a.goto(`/w/${ws}`)
  await expect(a.getByRole('link', { name: /People present: 2 of/ })).toBeVisible()

  // the register downloads as CSV
  await a.goto(`/w/${ws}/people/attendance`)
  const download = a.waitForEvent('download')
  await a.getByRole('button', { name: /^Download .* CSV$/ }).click()
  expect((await download).suggestedFilename()).toMatch(/^attendance-\d{4}-\d{2}\.csv$/)

  // members do not get the register or the owner tile
  await b.goto(`/w/${ws}/people/attendance`)
  await expect(b.getByRole('heading', { name: 'Monthly register' })).toHaveCount(0)
})
