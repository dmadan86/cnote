import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('customer raises a ticket, staff replies and resolves it, breach shows on home', async ({
  page,
  browser,
}) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  // the public ticket link shares the inquiry form's switch
  await page.goto(`${base}/crm/form`)
  await page.getByRole('button', { name: 'Create inquiry form' }).click()
  await expect(page.getByLabel('Public link')).toBeVisible()
  await page.goto(`${base}/crm/tickets/settings`)
  const link = await page.getByLabel('Public ticket link').inputValue()
  expect(link).toContain('/t/')
  await page.getByLabel('Urgent priority (hours)').fill('2')
  await page.getByRole('button', { name: 'Save hours' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Saved' })).toBeVisible()

  const visitor = await browser.newContext()
  const v = await visitor.newPage()
  await v.goto(link)
  const subject = `Wrong GST ${uniq()}`
  await v.getByLabel('Your name (required)').fill('Ravi Kumar')
  await v.getByLabel('Phone number (required)').fill('98765 43210')
  await v.getByLabel('Subject (required)').fill(subject)
  await v.getByLabel('What went wrong? (required)').fill('GST number on my invoice is wrong')
  await v.getByRole('button', { name: 'Send' }).click()
  await expect(v.getByRole('status')).toContainText('has your request')
  await visitor.close()

  await page.goto(`${base}/crm/tickets`)
  const list = page.getByRole('region', { name: 'Tickets' })
  await expect(list.getByRole('link', { name: subject })).toBeVisible()
  await expect(page.getByText('Due in')).toBeVisible()
  await list.getByRole('link', { name: subject }).click()
  await expect(page.getByText('GST number on my invoice is wrong')).toBeVisible()
  await page.getByLabel('Reply').fill('Fixing it today')
  await page.getByRole('button', { name: 'Send reply' }).click()
  await expect(page.getByText('Fixing it today')).toBeVisible()
  await page.getByLabel('Priority').selectOption('urgent')
  await expect(page.getByText('Urgent').first()).toBeVisible()

  // staff opens a second ticket for the same customer from the list
  await page.goto(`${base}/crm/tickets`)
  await page.getByRole('button', { name: 'New ticket' }).click()
  const d = page.getByRole('dialog', { name: 'New ticket' })
  await d.getByLabel('Subject').fill('Call back')
  await d.getByLabel('Customer').selectOption({ label: 'Ravi Kumar' })
  await d.getByRole('button', { name: 'Create ticket' }).click()
  await expect(list.getByRole('link', { name: 'Call back' })).toBeVisible()

  // resolving takes it off the active list
  await list.getByRole('link', { name: subject }).click()
  await expect(page.getByLabel('Assignee')).toBeVisible()
  await page.getByLabel('Status').selectOption('resolved')
  await expect(page.getByRole('status')).toHaveText('Saved')
  await page.goto(`${base}/crm/tickets`)
  await expect(list.getByRole('link', { name: subject })).toHaveCount(0)
  await page.getByRole('button', { name: 'Resolved' }).click()
  await expect(list.getByRole('link', { name: subject })).toBeVisible()

  // the home tile starts at zero
  await page.goto(base ?? '/')
  await expect(page.getByRole('link', { name: /Tickets breaching SLA: 0/ })).toBeVisible()
})
