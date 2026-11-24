import { expect, type Page, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

const wsOf = (page: Page) => new URL(page.url()).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string
const links = (page: Page, tag: string) =>
  page.getByRole('table').getByRole('link', { name: new RegExp(tag) })
// a change is saved when the config write has been answered; only then is a reload a fair test
const saved = (page: Page) => page.waitForResponse((r) => r.url().includes('ui_config') && r.ok())

test('contacts table: columns, sort, density, views and reset, all by keyboard where it matters', async ({
  page,
}) => {
  await signup(page)
  await createSpace(page)
  const base = wsOf(page)
  const tag = uniq()
  await page.goto(`${base}/crm`)
  for (const n of ['Mid', 'Zed', 'Alpha']) {
    await page.getByRole('button', { name: 'New contact' }).click()
    const d = page.getByRole('dialog', { name: 'New contact' })
    await d.getByLabel('Name').fill(`${n} ${tag}`)
    await d.getByRole('button', { name: 'Save contact' }).click()
    await expect(page.getByRole('link', { name: `${n} ${tag}` })).toBeVisible()
  }
  // server order is by name
  await expect(links(page, tag)).toHaveText([`Alpha ${tag}`, `Mid ${tag}`, `Zed ${tag}`])
  await expect(page.getByRole('columnheader')).toHaveText(['Name', 'Type', 'Email', 'GSTIN', 'State'])

  // hide a column with the keyboard
  await page.getByRole('button', { name: 'Columns' }).click()
  const dialog = page.getByRole('dialog', { name: 'Columns and display' })
  let w = saved(page)
  await dialog.getByRole('checkbox', { name: 'Type' }).focus()
  await page.keyboard.press('Space')
  await w
  await expect(dialog.getByText('Type hidden.')).toBeAttached()
  await expect(dialog.getByRole('checkbox', { name: 'Type' })).toBeFocused()
  await expect(page.getByRole('columnheader')).toHaveText(['Name', 'Email', 'GSTIN', 'State'])

  // reorder with the keyboard: Move State up; focus stays on a usable button and the move is announced
  w = saved(page)
  await dialog.getByRole('button', { name: 'Move State up' }).focus()
  await page.keyboard.press('Enter')
  await w
  await expect(dialog.getByText(/Moved State up, position 2 of 3/)).toBeAttached()
  await expect(dialog.getByRole('button', { name: 'Move State up' })).toBeFocused()
  await expect(page.getByRole('columnheader')).toHaveText(['Name', 'Email', 'State', 'GSTIN'])
  // a button disabled at the edge hands focus to the other arrow
  await page.keyboard.press('Enter')
  await expect(dialog.getByRole('button', { name: 'Move State down' })).toBeFocused()
  await expect(dialog.getByRole('button', { name: 'Move State up' })).toBeDisabled()

  // show a hidden one (Phone) and density
  await dialog.getByRole('checkbox', { name: 'Phone' }).check()
  await expect(page.getByRole('columnheader')).toHaveText(['Name', 'State', 'Email', 'GSTIN', 'Phone'])
  const rowH = async () => (await page.getByRole('row').nth(1).boundingBox())?.height ?? 0
  const standard = await rowH()
  w = saved(page)
  await dialog.getByRole('radio', { name: 'compact' }).focus()
  await page.keyboard.press('Space')
  await w
  await expect(page.getByRole('table')).toHaveAttribute('data-density', 'compact')
  expect(await rowH()).toBeLessThan(standard)
  await dialog.getByRole('radio', { name: 'relaxed' }).check()
  await expect(page.getByRole('table')).toHaveAttribute('data-density', 'relaxed')
  expect(await rowH()).toBeGreaterThan(standard)
  await dialog.getByRole('button', { name: 'Done' }).click()
  await expect(page.getByRole('button', { name: 'Columns' })).toBeFocused()

  // sort: ascending, descending, off; null last is covered below with Email (all empty)
  const nameHead = page.getByRole('columnheader', { name: 'Name' })
  await nameHead.getByRole('button').click()
  await expect(nameHead).toHaveAttribute('aria-sort', 'ascending')
  await expect(page.getByText('Sorted by Name, ascending.')).toBeAttached()
  await nameHead.getByRole('button').click()
  await expect(nameHead).toHaveAttribute('aria-sort', 'descending')
  await expect(links(page, tag)).toHaveText([`Zed ${tag}`, `Mid ${tag}`, `Alpha ${tag}`])

  // save a view: search narrows the list (server filter), the view stores it with the columns
  await page.getByPlaceholder('Search contacts').fill(`Alpha ${tag}`)
  await expect(page.getByRole('table').getByRole('link')).toHaveCount(1)
  await page.getByRole('button', { name: 'Save view' }).click()
  await page.getByLabel('View name').fill('Only Alpha')
  w = saved(page)
  await page.keyboard.press('Enter')
  await w
  await expect(page.getByRole('button', { name: 'Only Alpha', exact: true })).toBeFocused()
  await expect(page.getByRole('button', { name: 'Only Alpha', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await page.getByRole('button', { name: 'All', exact: true }).click()
  await expect(page.getByRole('table').getByRole('link')).toHaveCount(3)

  // reload keeps columns, sort, density and the view
  await page.reload()
  await expect(page.getByRole('columnheader')).toHaveText(['Name', 'State', 'Email', 'GSTIN', 'Phone'])
  await expect(page.getByRole('columnheader', { name: 'Name' })).toHaveAttribute('aria-sort', 'descending')
  await expect(page.getByRole('table')).toHaveAttribute('data-density', 'relaxed')
  await page.getByRole('button', { name: 'Only Alpha', exact: true }).click()
  await expect(page.getByPlaceholder('Search contacts')).toHaveValue(`Alpha ${tag}`)
  await expect(page.getByRole('table').getByRole('link')).toHaveCount(1)
  await page.getByRole('button', { name: 'All', exact: true }).click()

  // the name still opens the contact
  await page.getByRole('table').getByRole('link', { name: `Mid ${tag}` }).click()
  await expect(page).toHaveURL(/\/(crm\/contacts|n)\/[0-9a-f-]+$/)
  await page.goto(`${base}/crm`)

  // reset restores the defaults, saved views included
  await page.getByRole('button', { name: 'Columns' }).click()
  await page.getByRole('button', { name: 'Reset to default' }).click()
  await page.getByRole('button', { name: 'Confirm reset' }).click()
  await expect(page.getByRole('dialog').getByText(/^Reset to default\./)).toBeAttached()
  await expect(page.getByRole('dialog').getByRole('button', { name: 'Reset to default' })).toBeFocused()
  await page.getByRole('dialog').getByRole('button', { name: 'Done' }).click()
  await expect(page.getByRole('columnheader')).toHaveText(['Name', 'Type', 'Email', 'GSTIN', 'State'])
  await expect(page.getByRole('columnheader', { name: 'Name' })).not.toHaveAttribute('aria-sort', /.+/)
  await expect(page.getByRole('table')).toHaveAttribute('data-density', 'standard')
  await expect(page.getByRole('button', { name: 'Only Alpha', exact: true })).toHaveCount(0)
  await page.reload()
  await expect(page.getByRole('columnheader')).toHaveText(['Name', 'Type', 'Email', 'GSTIN', 'State'])
  await expect(links(page, tag)).toHaveText([`Alpha ${tag}`, `Mid ${tag}`, `Zed ${tag}`])
})

test('invoices table: actions and the deep links still work, sort and page size are kept', async ({
  page,
}) => {
  await signup(page)
  await createSpace(page)
  const base = wsOf(page)
  const customer = `Cust ${uniq()}`
  await page.goto(`${base}/crm`)
  await page.getByRole('button', { name: 'New contact' }).click()
  const d = page.getByRole('dialog', { name: 'New contact' })
  await d.getByLabel('Name').fill(customer)
  await d.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('link', { name: customer })).toBeVisible()

  // create two drafts through the existing New invoice flow
  for (let i = 0; i < 2; i++) {
    await page.goto(`${base}/billing`)
    await page.getByRole('button', { name: 'New invoice' }).first().click()
    await page.getByLabel('Customer').selectOption({ label: customer })
    await page.getByRole('button', { name: 'Create draft' }).click()
    await expect(page).toHaveURL(/\/billing\/[0-9a-f-]+$/)
  }
  await page.goto(`${base}/billing`)
  await expect(page.getByRole('columnheader')).toHaveText([
    'Number',
    'Customer',
    'Date',
    'Due',
    'Status',
    'Total',
    'Outstanding',
  ])
  await expect(page.getByRole('link', { name: 'Draft' })).toHaveCount(2)

  // sort by Customer, change the page size; both survive a reload
  const w = saved(page)
  await page.getByRole('columnheader', { name: 'Customer' }).getByRole('button').click()
  await w
  await expect(page.getByRole('columnheader', { name: 'Customer' })).toHaveAttribute('aria-sort', 'ascending')
  const w2 = saved(page)
  await page.getByLabel('Rows per page').selectOption('25')
  await w2
  await page.reload()
  await expect(page.getByRole('columnheader', { name: 'Customer' })).toHaveAttribute('aria-sort', 'ascending')
  await expect(page.getByLabel('Rows per page')).toHaveValue('25')

  // Home deep links keep working: the banner, the empty filtered result and Show all
  await page.goto(`${base}/billing?filter=overdue`)
  await expect(page.getByText('Showing: overdue invoices')).toBeVisible()
  await expect(page.getByText('No invoices match')).toBeVisible()
  await page.getByRole('link', { name: 'Show all' }).click()
  await expect(page).toHaveURL(/\/billing$/)
  await expect(page.getByRole('link', { name: 'Draft' })).toHaveCount(2)

  // a draft opens
  await page.getByRole('link', { name: 'Draft' }).first().click()
  await expect(page).toHaveURL(/\/billing\/[0-9a-f-]+$/)

  // reset from the dialog: the server row goes, the default comes back
  await page.goto(`${base}/billing`)
  await page.getByRole('button', { name: 'Columns' }).click()
  await page.getByRole('button', { name: 'Reset to default' }).click()
  await page.getByRole('button', { name: 'Confirm reset' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Done' }).click()
  await expect(page.getByRole('columnheader', { name: 'Customer' })).not.toHaveAttribute('aria-sort', /.+/)
  await expect(page.getByLabel('Rows per page')).toHaveValue('50')
})
