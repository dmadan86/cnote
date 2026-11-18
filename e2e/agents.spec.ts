import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('Settings → Agents: install, toggle, run now, and the lead responder reacts to an inquiry', async ({
  page,
  browser,
}) => {
  await signup(page)
  await createSpace(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
  const ws = base.split('/')[2]
  const ch = await page.request.post(`/api/w/${ws}/commands/chat.channel_create`, { data: { name: 'team' } })
  expect(ch.ok()).toBeTruthy()
  await page.goto(`${base}/settings?tab=Agents`)
  const cards = page.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Install' }) })
  await expect(cards).toHaveCount(4)

  // Daily briefing: pick a channel.
  await cards.filter({ hasText: 'Daily briefing' }).getByRole('button', { name: 'Install' }).click()
  const dlg = page.getByRole('dialog', { name: /Add: Daily briefing/ })
  await dlg.getByLabel('Channel').selectOption({ index: 1 })
  await dlg.getByRole('button', { name: 'Add employee' }).click()
  await expect(dlg).toHaveCount(0)
  const brief = page.getByRole('listitem').filter({ hasText: 'Daily briefing' })
  const sw = brief.getByRole('switch', { name: 'Daily briefing enabled' })
  const was = await sw.isChecked()
  await sw.click()
  await expect(sw).toBeChecked({ checked: !was })
  if (!(await sw.isChecked())) await sw.click()
  await expect(sw).toBeChecked()

  await brief.getByRole('link', { name: 'Open' }).click()
  await page.getByRole('button', { name: 'Run now' }).click()
  await expect(page.getByText('Started a run.')).toBeVisible()
  await expect(page.getByRole('table')).toContainText(
    /Queued|Running|Done|Failed|Waiting for approval|Skipped/,
    {
      timeout: 30_000,
    },
  )

  // Lead responder reacts to a public inquiry.
  await page.goto(`${base}/settings?tab=Agents`)
  await page
    .getByRole('listitem')
    .filter({ hasText: 'Lead responder' })
    .getByRole('button', { name: 'Install' })
    .click()
  await page
    .getByRole('dialog', { name: /Add: Lead responder/ })
    .getByRole('button', { name: 'Add employee' })
    .click()
  const lr = page.getByRole('listitem').filter({ hasText: 'Lead responder' })
  await expect(lr.getByRole('link', { name: 'Open' })).toBeVisible()

  await page.goto(`${base}/crm/form`)
  await page.getByRole('button', { name: 'Create inquiry form' }).click()
  const link = await page.getByLabel('Public link').inputValue()
  const visitor = await browser.newContext()
  const v = await visitor.newPage()
  await v.goto(link)
  await v.getByLabel('Your name').fill(`Meera ${uniq()}`)
  await v.getByLabel('Phone number').fill('098765 43210')
  await v.getByLabel('How can we help?').fill('Need a quote for 20 chairs')
  await v.getByRole('button', { name: 'Send' }).click()
  await expect(v.getByRole('status')).toContainText('has your message')
  await visitor.close()

  const lr2 = page.getByRole('listitem').filter({ hasText: 'Lead responder' })
  await expect(async () => {
    await page.goto(`${base}/settings?tab=Agents`)
    await expect(
      lr2.getByText(/Queued|Running|Done|Failed|Waiting for approval|Skipped/).first(),
    ).toBeVisible({
      timeout: 2000,
    })
  }).toPass({ timeout: 30_000 })
  await lr2.getByRole('link', { name: 'Open' }).click()
  await expect(page.getByRole('table')).toContainText(/Event/, { timeout: 30_000 })

  // A held draft, if the run produced one, is approved on its card.
  await page.goto(`${base}/approvals`)
  const approve = page.getByRole('button', { name: /^Approve/ }).first()
  if (await approve.isVisible({ timeout: 3000 }).catch(() => false)) {
    await approve.click()
  }
})
