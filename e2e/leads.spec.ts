import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

const today = () => {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

test('public form lead is answered, followed up and lost with a reason', async ({ page, browser }) => {
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]

  // admin creates the form and copies its link
  await page.goto(`${base}/crm/form`)
  await page.getByRole('button', { name: 'Create inquiry form' }).click()
  const link = await page.getByLabel('Public link').inputValue()
  expect(link).toContain('/f/')

  // a visitor with no session submits it
  const visitor = await browser.newContext()
  const v = await visitor.newPage()
  await v.goto(link)
  await expect(v.getByText('will use your details only to respond')).toBeVisible()
  const name = `Meera ${uniq()}`
  await v.getByLabel('Your name').fill(name)
  await v.getByLabel('Phone number').fill('098765 43210')
  await v.getByLabel('How can we help?').fill('Need a quote for 20 chairs')
  await v.getByRole('button', { name: 'Send' }).click()
  await expect(v.getByRole('status')).toContainText('has your message')
  await visitor.close()

  // it shows on the home tile and in the Inbox with the message and a call link
  await page.goto(base ?? '/')
  await expect(page.getByRole('link', { name: /New inquiries: 1/ })).toBeVisible()
  await page.goto(`${base}/crm/inbox`)
  const card = page
    .getByRole('region', { name: 'New inquiries' })
    .getByRole('listitem')
    .filter({ hasText: name })
  await expect(card).toContainText('Need a quote for 20 chairs')
  await expect(card.getByRole('link', { name: '+919876543210' })).toHaveAttribute('href', 'tel:+919876543210')

  // log the call with a follow-up today: it leaves the Inbox and lands under Today
  await card.getByRole('button', { name: 'Log outcome' }).click()
  const d = page.getByRole('dialog', { name: /Log activity/ })
  await d.getByLabel('Summary (optional)').fill('Called, wants site visit Friday')
  await d.getByLabel('Next step (optional)').fill('Site visit')
  await d.getByLabel('Follow up on').fill(today())
  await d.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('No unanswered inquiries.')).toBeVisible()

  await page.getByRole('link', { name: 'Follow-ups' }).click()
  await expect(page.getByTestId('count-today')).toHaveText('1')
  const row = page
    .getByRole('region', { name: 'Follow-ups' })
    .getByRole('listitem')
    .filter({ hasText: `Inquiry from ${name}` })
  await expect(row).toContainText('Site visit')
  await page.goto(base ?? '/')
  await expect(page.getByRole('link', { name: /Follow-ups: 1/ })).toBeVisible()

  // snooze to tomorrow, undo, then open the deal: timeline shows the call
  await page.goto(`${base}/crm/follow-ups`)
  await row.getByRole('button', { name: /Snooze to tomorrow/ }).click()
  await expect(page.getByRole('status').first()).toContainText('moved to')
  await page.getByRole('button', { name: 'Undo' }).click()
  await expect(page.getByTestId('count-today')).toHaveText('1')
  await row.getByRole('link', { name: `Inquiry from ${name}` }).click()
  await expect(page.getByRole('region', { name: 'Activity' })).toContainText(
    'Called, wants site visit Friday',
  )
  await expect(page.getByRole('region', { name: 'Next step' })).toContainText('Next: Site visit')

  // customer 360 shows the same call
  await page.getByRole('link', { name: `Open ${name}` }).click()
  await expect(page.getByRole('region', { name: 'Timeline' })).toContainText(
    'Called, wants site visit Friday',
  )

  // lost needs a reason; cancelling leaves the deal open
  await page.goBack()
  await page.getByLabel('Stage').selectOption('lost')
  await page.getByRole('button', { name: 'Save deal' }).click()
  const sheet = page.getByRole('dialog', { name: /Why was .* lost/ })
  await expect(sheet.getByRole('button', { name: 'Mark as lost' })).toBeDisabled()
  await sheet.getByRole('button', { name: 'Cancel' }).click()
  await page.reload()
  await expect(page.getByLabel('Stage')).toHaveValue('lead')
  await page.getByLabel('Stage').selectOption('lost')
  await page.getByRole('button', { name: 'Save deal' }).click()
  await page
    .getByRole('dialog', { name: /Why was .* lost/ })
    .getByLabel('Price too high')
    .check()
  await page.getByRole('button', { name: 'Mark as lost' }).click()
  await expect(page.getByLabel('Stage')).toHaveValue('lost')
  await expect(page.getByRole('region', { name: 'Next step' })).toContainText('Closed')
})

test('dragging a card to Lost asks for a reason first', async ({ page }) => {
  await page.setViewportSize({ width: 1800, height: 900 })
  await signup(page)
  await createSpace(page)
  const base = new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0]
  await page.goto(`${base}/crm/deals`)
  await page
    .getByRole('region', { name: 'Lead stage' })
    .getByRole('button', { name: /New deal/ })
    .click()
  const title = `Drag ${uniq()}`
  await page
    .getByRole('dialog', { name: /New deal/ })
    .getByLabel('Deal title')
    .fill(title)
  await page
    .getByRole('dialog', { name: /New deal/ })
    .getByRole('button', { name: 'Create deal' })
    .click()
  const card = page.getByLabel(`Deal ${title}`)
  await expect(card).toBeVisible()
  const from = (await card.boundingBox())!
  const to = (await page.getByRole('region', { name: 'Lost stage' }).boundingBox())!
  await page.mouse.move(from.x + 10, from.y + from.height - 6)
  await page.mouse.down()
  await page.mouse.move(from.x + 40, from.y + 40, { steps: 5 })
  await page.mouse.move(to.x + 40, to.y + 60, { steps: 15 })
  await page.mouse.up()
  await page.waitForTimeout(100) // dnd-kit swallows clicks for 50ms after a drop
  const sheet = page.getByRole('dialog', { name: /Why was .* lost/ })
  await expect(sheet).toBeVisible()
  await expect(page.getByTestId('count-lost')).toHaveText('0') // nothing moved yet
  await sheet.getByRole('radio', { name: 'Other' }).check()
  await sheet.getByRole('textbox').fill('Closed the shop')
  await sheet.getByRole('button', { name: 'Mark as lost' }).click()
  await expect(page.getByTestId('count-lost')).toHaveText('1')
})
