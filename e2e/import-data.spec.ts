import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

const wsBase = (url: string) => new URL(url).pathname.match(/^\/w\/[0-9a-f-]+/)?.[0] as string

test('contacts CSV wizard: bad GSTIN and duplicate are sorted out, then undo', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const base = wsBase(page.url())
  const tag = uniq()

  // One contact already exists, so its row lands under "Already in C Note".
  await page.goto(`${base}/crm`)
  await page.getByRole('button', { name: 'New contact' }).click()
  const cd = page.getByRole('dialog', { name: 'New contact' })
  await cd.getByLabel('Name').fill(`Dup ${tag}`)
  await cd.getByLabel('Email').fill(`dup-${tag}@example.test`)
  await cd.getByRole('button', { name: 'Save contact' }).click()
  await expect(page.getByRole('table').getByRole('link', { name: `Dup ${tag}` })).toBeVisible()

  const csv = [
    'Party Name,Type,Email,Phone,GSTIN',
    `Alpha ${tag},customer,alpha-${tag}@example.test,9876500001,`,
    `Beta ${tag},vendor,beta-${tag}@example.test,9876500002,`,
    `Badgst ${tag},customer,bad-${tag}@example.test,9876500003,27AAPFU0939F1ZX`,
    `Dup ${tag},customer,dup-${tag}@example.test,,`,
  ].join('\n')

  await page.getByRole('link', { name: 'Import' }).click()
  await expect(page).toHaveURL(/\/import\/contacts$/)
  await page.getByLabel('CSV file').setInputFiles({
    name: 'contacts.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from(csv),
  })

  // Match columns: everything is recognised from the header names.
  await expect(page.getByText('0 unmatched')).toBeVisible()
  await page.getByRole('button', { name: 'Continue to review' }).click()

  await expect(page.getByRole('tab', { name: /^Ready 2/ })).toBeVisible()
  await page.getByRole('tab', { name: /^Needs fixing 1/ }).click()
  await expect(page.getByRole('row', { name: new RegExp(`Badgst ${tag}`) })).toBeVisible()
  await page.getByRole('tab', { name: /^Already in C Note 1/ }).click()
  await expect(page.getByRole('row', { name: new RegExp(`Dup ${tag}`) })).toBeVisible()

  await page.getByRole('button', { name: 'Import 2 rows' }).click()
  await expect(page.getByText('Import finished.')).toBeVisible({ timeout: 30_000 })
  await expect(page.getByText('Added', { exact: true }).locator('xpath=following-sibling::div')).toHaveText(
    '2',
  )

  await page.getByRole('link', { name: 'View contacts' }).click()
  await expect(page.getByRole('table').getByRole('link', { name: `Alpha ${tag}` })).toBeVisible()

  // Undo from the hub.
  await page.goto(`${base}/import`)
  const recent = page.getByRole('region', { name: 'Recent imports' })
  await recent.getByRole('button', { name: 'Undo' }).first().click()
  const dlg = page.getByRole('dialog', { name: 'Undo this import?' })
  await expect(dlg).toContainText('7 days')
  await dlg.getByRole('button', { name: 'Undo import' }).click()
  await expect(recent.getByText('Undone')).toBeVisible()
  await page.goto(`${base}/crm`)
  await expect(page.getByRole('table').getByRole('link', { name: `Dup ${tag}` })).toBeVisible()
  await expect(page.getByRole('table').getByRole('link', { name: `Alpha ${tag}` })).toHaveCount(0)
})

test('opening balances post with a difference to adjustments, then reverse', async ({ page }) => {
  await signup(page)
  const base = wsBase(page.url())
  await page.goto(`${base}/import/opening`)
  await page.getByLabel('Bank / UPI clearing debit').first().fill('10000')
  await expect(page.getByTestId('difference')).toHaveText('₹10,000.00')
  await page.getByRole('button', { name: 'Save draft' }).click()
  await expect(page.getByText('Draft saved')).toBeVisible()

  await page.getByRole('button', { name: 'Post opening balances' }).click()
  const dlg = page.getByRole('dialog', { name: 'Post opening balances?' })
  await expect(dlg).toContainText('₹10,000.00')
  await dlg.getByRole('button', { name: 'Confirm and post' }).click()
  await expect(page.getByText('Opening balances posted.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Reverse' })).toBeVisible()

  await page.getByRole('button', { name: 'Reverse' }).click()
  await expect(page.getByText(/reversed/i).first()).toBeVisible()
})

const TALLY = `<?xml version="1.0" encoding="UTF-8"?>
<ENVELOPE><BODY><IMPORTDATA><REQUESTDATA>
<TALLYMESSAGE xmlns:UDF="TallyUDF">
<LEDGER NAME="Sharma Traders" RESERVEDNAME=""><PARENT>Sundry Debtors</PARENT><OPENINGBALANCE>-11800.00</OPENINGBALANCE><LEDSTATENAME>Maharashtra</LEDSTATENAME></LEDGER>
<LEDGER NAME="Gupta Supplies" RESERVEDNAME=""><PARENT>Sundry Creditors</PARENT><OPENINGBALANCE>5000.00</OPENINGBALANCE></LEDGER>
<LEDGER NAME="Cash" RESERVEDNAME=""><PARENT>Cash-in-Hand</PARENT><OPENINGBALANCE>-2500.00</OPENINGBALANCE></LEDGER>
<UNIT NAME="Nos"><ISSIMPLEUNIT>Yes</ISSIMPLEUNIT></UNIT>
<STOCKITEM NAME="Steel widget"><PARENT/><BASEUNITS>Nos</BASEUNITS><OPENINGBALANCE>10 Nos</OPENINGBALANCE><OPENINGVALUE>-1000.00</OPENINGVALUE></STOCKITEM>
</TALLYMESSAGE></REQUESTDATA></IMPORTDATA></BODY></ENVELOPE>`

test('Tally masters upload shows what we found', async ({ page }) => {
  await signup(page)
  const base = wsBase(page.url())
  await page.goto(`${base}/import`)
  await page.getByRole('link', { name: /From Tally/ }).click()
  await expect(page.getByText('Alt+E')).toBeVisible()
  await page.getByLabel('Tally XML file').setInputFiles({
    name: 'masters.xml',
    mimeType: 'text/xml',
    buffer: Buffer.from(TALLY),
  })
  await expect(page.getByRole('heading', { name: 'What we found' })).toBeVisible()
  await expect(page.getByText(/1 customers/)).toBeVisible()
  await expect(page.getByText(/1 vendors/)).toBeVisible()
  await expect(page.getByText(/1 stock items/)).toBeVisible()
  await page.getByRole('button', { name: 'Continue to review' }).click()
  await expect(page.getByRole('tab', { name: /^Ready/ })).toBeVisible()
})
