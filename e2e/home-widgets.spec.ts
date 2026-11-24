import { expect, type Page, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

// The configurable Home exists only when the servers run with HOME_WIDGETS=1 (CI does; this process gets it too).
const on = process.env.HOME_WIDGETS === '1'

const order = (page: Page) =>
  page
    .getByRole('list', { name: 'Home widgets' })
    .getByRole('listitem')
    .evaluateAll((els) => els.map((e) => e.querySelector('span.font-medium')?.textContent ?? ''))

const wsOf = (page: Page) => new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string

test.describe('configurable Home', () => {
  test.skip(!on, 'needs HOME_WIDGETS=1')

  test('owner customises with the keyboard, the layout survives a reload, reset restores the default', async ({
    page,
  }) => {
    await signup(page)
    await createSpace(page)
    const base = `/w/${wsOf(page)}`
    await page.goto(base)
    await page.getByRole('button', { name: 'Customise' }).click()
    await expect(page.getByRole('heading', { name: 'Customise Home' })).toBeFocused()

    // add from the gallery
    await page.getByRole('button', { name: 'Add a widget' }).click()
    await page.getByRole('button', { name: 'Add Quick links' }).click()
    // focus moves to another Add button (the clicked one became "Added") and the addition is announced
    await expect(page.getByRole('dialog').getByText('Added Quick links.')).toBeAttached()
    await expect(page.getByRole('dialog').getByRole('button', { name: /^Add / })).not.toHaveCount(0)
    await expect
      .poll(() => page.evaluate(() => document.activeElement?.getAttribute('aria-label') ?? ''))
      .toMatch(/^Add /)
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).last().click()
    await expect(page.getByRole('button', { name: 'Move Quick links', exact: true })).toBeFocused()

    // move by keyboard: Space lifts, ArrowUp moves, Space drops
    const before = await order(page)
    await page.getByRole('button', { name: 'Move Sales trend', exact: true }).focus()
    await page.keyboard.press('Space')
    await page.waitForTimeout(150)
    await page.keyboard.press('ArrowUp')
    await page.waitForTimeout(150)
    await page.keyboard.press('Space')
    await expect
      .poll(async () => (await order(page)).indexOf('Sales trend'))
      .toBe(before.indexOf('Sales trend') - 1)

    // the move buttons announce and disable at the edge
    await page.getByRole('button', { name: 'Move Sales trend down' }).click()
    await expect(page.getByText(/Moved Sales trend down/)).toBeAttached()
    await expect(page.getByRole('button', { name: 'Move Ask your business up' })).toBeDisabled()

    // resize with the radio group, by keyboard
    const medium = page
      .getByRole('radiogroup', { name: 'Size of Sales trend' })
      .getByRole('radio', { name: 'Medium' })
    await expect(medium).toBeChecked()
    await medium.focus()
    await page.keyboard.press('ArrowRight')
    await expect(
      page.getByRole('radiogroup', { name: 'Size of Sales trend' }).getByRole('radio', { name: 'Large' }),
    ).toBeChecked()

    // remove
    await page.getByRole('button', { name: 'Remove Ask your business' }).click()
    const edited = await order(page)
    expect(edited).not.toContain('Ask your business')
    await page.getByRole('button', { name: 'Done' }).click()
    await expect(page.getByRole('button', { name: 'Customise' })).toBeFocused()
    await expect(page.getByRole('form', { name: 'Ask your business' })).toHaveCount(0)
    await expect(page.getByRole('link', { name: 'Invoices', exact: true })).toBeVisible() // quick links

    // reload keeps it
    await page.reload()
    await page.getByRole('button', { name: 'Customise' }).click()
    await expect(page.getByRole('heading', { name: 'Customise Home' })).toBeVisible() // the editor loads lazily
    expect(await order(page)).toEqual(edited)
    await expect(
      page.getByRole('radiogroup', { name: 'Size of Sales trend' }).getByRole('radio', { name: 'Large' }),
    ).toBeChecked()

    // reset restores the default
    await page.getByRole('button', { name: 'Reset to default' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Reset', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Customise' })).toBeFocused()
    await expect(page.getByRole('form', { name: 'Ask your business' })).toBeVisible()
    await page.reload()
    await expect(page.getByRole('form', { name: 'Ask your business' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Invoices', exact: true })).toHaveCount(0)
  })

  test('a failed reset keeps the layout that is stored and shows the error', async ({ page }) => {
    await signup(page)
    await createSpace(page)
    await page.goto(`/w/${wsOf(page)}`)
    await page.getByRole('button', { name: 'Customise' }).click()
    await page.getByRole('button', { name: 'Remove Ask your business' }).click()
    await page.getByRole('button', { name: 'Done' }).click()
    await expect(page.getByRole('form', { name: 'Ask your business' })).toHaveCount(0)
    await page.route('**/commands/ui_config.reset', (r) => r.fulfill({ status: 500, body: '{}' }))
    await page.getByRole('button', { name: 'Customise' }).click()
    await page.getByRole('button', { name: 'Reset to default' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Reset', exact: true }).click()
    await expect(page.getByRole('alert').filter({ hasText: /\S/ })).toBeVisible() // Next adds an empty route-announcer alert
    await page.getByRole('button', { name: 'Cancel' }).click()
    // still the custom layout, not the defaults
    await expect(page.getByRole('form', { name: 'Ask your business' })).toHaveCount(0)
    await page.unroute('**/commands/ui_config.reset')
    await page.reload()
    await expect(page.getByRole('form', { name: 'Ask your business' })).toHaveCount(0)
  })

  test('a member without finance access sees no finance widgets', async ({ browser }) => {
    const oc = await browser.newContext()
    const mc = await browser.newContext()
    const owner = await oc.newPage()
    const member = await mc.newPage()
    try {
      await signup(owner)
      await createSpace(owner)
      const ws = wsOf(owner)
      const call = async (name: string, data: unknown) => {
        const r = await owner.request.post(`/api/w/${ws}/commands/${name}`, { data })
        expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
        return r.json()
      }
      const mu = await signup(member)
      await call('member.add', { email: mu.email, role: 'member' })
      const row = (await call('member.list', {})).find((m: { email: string }) => m.email === mu.email)
      await call('member.update', { user_id: row.user_id, finance_access: false })

      await member.goto(`/w/${ws}`)
      await member.getByRole('button', { name: 'Customise' }).click()
      await expect(member.getByRole('heading', { name: 'Customise Home' })).toBeVisible()
      const money =
        /Sales this month|Sales trend|Receivables ageing|Top customers|Expenses by category|Payments pending|Bills due/
      await expect(member.getByRole('list', { name: 'Home widgets' }).getByText(money)).toHaveCount(0)
      await member.getByRole('button', { name: 'Add a widget' }).click()
      const gallery = member.getByRole('dialog', { name: 'Add a widget' })
      await expect(gallery.getByText('Tasks due')).toBeVisible()
      await expect(gallery.getByText(money)).toHaveCount(0)
    } finally {
      await oc.close()
      await mc.close()
    }
  })

  test('on a phone it is one column and reorders with the move buttons', async ({ browser }) => {
    const ctx = await browser.newContext({ viewport: { width: 390, height: 800 } })
    const page = await ctx.newPage()
    try {
      await signup(page) // no createSpace: the sidebar is a drawer on a phone
      const region = page.getByRole('region', { name: 'Today at a glance' })
      await expect(region).toBeVisible()
      const lefts = await region
        .locator(':scope > div')
        .evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().left)))
      expect(new Set(lefts).size).toBe(1)
      await page.getByRole('button', { name: 'Customise' }).click()
      await expect(page.getByRole('heading', { name: 'Customise Home' })).toBeVisible() // the editor loads lazily
      const before = await order(page)
      await page.getByRole('button', { name: `Move ${before[1]} down` }).click()
      expect((await order(page))[2]).toBe(before[1])
    } finally {
      await ctx.close()
    }
  })
})

test('with the flag off Home is the standard one', async ({ page }) => {
  test.skip(on, 'runs only without HOME_WIDGETS')
  await signup(page)
  await createSpace(page)
  await expect(page.getByRole('region', { name: 'Today at a glance' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Customise' })).toHaveCount(0)
})
