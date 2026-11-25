import { expect, test } from '@playwright/test'
import { gridOf, templateBase } from './tables-helpers'

const NOT_AVAILABLE = 'This form is not available.'

/** Builds a form from the Forms dialog and returns its link (shown once). */
async function buildForm(page: import('@playwright/test').Page, name: string) {
  await page.getByRole('button', { name: 'Forms' }).click()
  const d = page.getByRole('dialog', { name: 'Forms' })
  await d.getByRole('button', { name: 'New form' }).click()
  await d.getByLabel('Form name').fill(name)
  await d.getByRole('button', { name: 'Create form' }).click()
  await expect(d.getByText('Copy it now; you can make a new link later.')).toBeVisible()
  const url = await d.getByLabel('Form link').inputValue()
  expect(url).toContain('/tf/')
  await d.getByRole('button', { name: 'Done' }).click()
  return { d, url }
}

test('build a form, submit it signed out, the record lands in the grid; required is enforced', async ({
  page,
  browser,
}) => {
  await templateBase(page)
  const { d, url } = await buildForm(page, 'Order intake')

  // edit: intro and thank-you text
  const form = d
    .getByRole('list', { name: 'Forms' })
    .getByRole('listitem')
    .filter({ hasText: 'Order intake' })
  await form.getByRole('button', { name: 'Edit' }).click()
  await d.getByLabel('Intro text').fill('Tell us what you need.')
  await d.getByLabel('Thank-you text').fill('Got it, we will call you.')
  // the template's primary field is an auto number, so nothing is required until the manager says so
  await d.getByRole('listitem').filter({ hasText: 'Label for Item' }).last().getByLabel('Required').check()
  await d.getByRole('button', { name: 'Save form' }).click()
  await expect(d.getByText('0 responses')).toBeVisible()
  await d.getByRole('button', { name: 'Close' }).first().click()

  const ctx = await browser.newContext()
  const pub = await ctx.newPage()
  await pub.goto(url)
  await expect(pub.getByRole('heading', { name: 'Order intake' })).toBeVisible()
  await expect(pub.getByText('Tell us what you need.')).toBeVisible()
  // chrome-free and the honeypot is out of reach of people and assistive technology
  await expect(pub.getByRole('navigation')).toHaveCount(0)
  await expect(pub.locator('input[name=hp]')).toHaveAttribute('autocomplete', 'off')
  await expect(pub.locator('input[name=hp]')).toHaveAttribute('tabindex', '-1')
  await expect(pub.getByRole('textbox', { name: 'hp' })).toHaveCount(0)

  // required field: empty submit is refused with an error tied to the field
  await pub.getByRole('button', { name: 'Submit' }).click()
  await expect(pub.getByRole('form', { name: 'Order intake' }).getByRole('alert')).toContainText(
    'Item is required.',
  )
  await expect(pub.getByLabel(/^Item/)).toHaveAttribute('aria-invalid', 'true')

  await pub.getByLabel(/^Item/).fill('Brass lamp, 3')
  await pub.getByRole('button', { name: 'Submit' }).click()
  await expect(pub.getByRole('status')).toContainText('Got it, we will call you.')
  await ctx.close()

  await page.reload()
  await expect(gridOf(page).getByRole('gridcell', { name: 'Brass lamp, 3' })).toBeVisible()
})

test('a rotated link and a disabled form both show "isn\'t available"', async ({ page, browser }) => {
  await templateBase(page)
  const { d, url } = await buildForm(page, 'Quick form')
  const ctx = await browser.newContext()
  const pub = await ctx.newPage()
  const gone = async (u: string) => {
    await pub.goto(u)
    await expect(pub.getByText(NOT_AVAILABLE)).toBeVisible()
  }
  await pub.goto(url)
  await expect(pub.getByRole('heading', { name: 'Quick form' })).toBeVisible()

  // rotate (with confirm): the old link dies, the new one works
  const item = d.getByRole('list', { name: 'Forms' }).getByRole('listitem').filter({ hasText: 'Quick form' })
  await item.getByRole('button', { name: 'New link' }).click()
  await item.getByRole('button', { name: 'Make new link' }).click()
  const fresh = await d.getByLabel('Form link').inputValue()
  expect(fresh).not.toBe(url)
  await gone(url)
  await pub.goto(fresh)
  await expect(pub.getByRole('heading', { name: 'Quick form' })).toBeVisible()
  await d.getByRole('button', { name: 'Done' }).click()

  // disable (with confirm)
  await item.getByRole('button', { name: 'Turn off' }).click()
  await item.getByRole('button', { name: 'Turn off' }).click()
  await expect(item.getByText('Off', { exact: true })).toBeVisible()
  await gone(fresh)
  await ctx.close()
})
