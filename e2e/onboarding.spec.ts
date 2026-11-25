import { expect, type Page, test } from '@playwright/test'
import { newUser, signup, uniq } from './helpers'

// The "Get set up" guide is a widget of the configurable Home (CI starts the servers with HOME_WIDGETS=1).
const homeWidgets = process.env.HOME_WIDGETS === '1'

/** The page's own alerts, not Next's route announcer. */
const alert = (page: Page) => page.locator('form [role="alert"]')
const wsOf = (page: Page) => new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
const run = async <T>(page: Page, name: string, data: unknown = {}) => {
  const r = await page.request.post(`/api/w/${wsOf(page)}/commands/${name}`, { data })
  expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
  return (await r.json()) as T
}
const doneAt = async (page: Page) =>
  (await run<{ settings: { onboarding?: { done_at?: string } } }>(page, 'workspace.get')).settings.onboarding
    ?.done_at

// A valid GSTIN needs its check character.
const CHARS = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'
function gstin(state: string, pan: string) {
  const body = `${state}${pan}1Z`
  const sum = [...body].reduce((s, ch, i) => {
    const v = CHARS.indexOf(ch) * (i % 2 ? 2 : 1)
    return s + Math.floor(v / 36) + (v % 36)
  }, 0)
  return body + CHARS[(36 - (sum % 36)) % 36]
}

test('full run: business, details, first goal, and the chosen flow opens', async ({ page }) => {
  await signup(page, undefined, { wizard: true })
  await expect(page.getByText('Step 1 of 3')).toBeVisible()
  await page.getByRole('radio', { name: /Service company/ }).check({ force: true })
  await page.getByLabel('Business name').fill('Sharma Electricals')
  await page.getByRole('button', { name: 'Continue' }).click()

  await expect(page.getByRole('heading', { name: /Your business details/ })).toBeFocused()
  await expect(page.getByText('Step 2 of 3')).toBeVisible()
  // the state is required, the GSTIN is checked as typed
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(alert(page)).toHaveText('Choose your state.')
  const g = page.getByLabel('GSTIN (optional)')
  await g.fill('27abc')
  await g.blur()
  await expect(alert(page).filter({ hasText: 'This GSTIN is not 15' })).toBeVisible()
  await g.fill('27AAAAA0000A1Y1')
  await g.blur()
  await expect(alert(page).filter({ hasText: 'does not look right' })).toBeVisible()
  await g.fill(gstin('27', 'AAAAA0000A'))
  await expect(page.getByLabel('State')).toHaveValue('27')
  await expect(page.getByLabel('State')).toBeDisabled()
  await expect(page.getByText('Set from your GSTIN')).toBeVisible()
  await page.getByLabel('Address (optional)').fill('12 MG Road, Pune')
  await page.getByRole('button', { name: 'Continue' }).click()

  await expect(page.getByRole('heading', { name: /What do you want to do first/ })).toBeFocused()
  await page.getByRole('button', { name: 'Finish' }).click()
  await expect(alert(page)).toHaveText('Pick one to start with.')
  await page.getByRole('radio', { name: 'Add customers' }).check()
  await page.getByRole('button', { name: 'Finish' }).click()

  // the new-contact dialog opens by itself, and the address bar is tidied
  await expect(page.getByRole('dialog', { name: 'New contact' })).toBeVisible()
  await expect(page).toHaveURL(/\/w\/[0-9a-f-]+\/crm$/)

  const co = await run<Record<string, string>>(page, 'company.get')
  expect(co).toMatchObject({
    legal_name: 'Sharma Electricals',
    state_code: '27',
    address: '12 MG Road, Pune',
  })
  const ws = await run<{ settings: { business_type: string; first_goal: string } }>(page, 'workspace.get')
  expect(ws.settings).toMatchObject({ business_type: 'service', first_goal: 'customers' })
  expect(await doneAt(page)).toBeTruthy()
})

for (const [skipAt, label] of [
  [1, 'the first step'],
  [2, 'the second step'],
  [3, 'the last step'],
] as const) {
  test(`Skip for now on ${label} reaches Home, and setup does not come back`, async ({ page }) => {
    await signup(page, undefined, { wizard: true })
    for (let s = 1; s < skipAt; s++) {
      if (s === 1) await page.getByRole('radio', { name: /Trading company/ }).check({ force: true })
      if (s === 2) await page.getByLabel('State').selectOption('29')
      await page.getByRole('button', { name: 'Continue' }).click()
      await expect(page.getByText(`Step ${s + 1} of 3`)).toBeVisible()
    }
    await page.getByRole('button', { name: 'Skip for now' }).click()
    await expect(page).toHaveURL(/\/w\/[0-9a-f-]+$/)
    expect(await doneAt(page)).toBeTruthy()
    await page.reload()
    await expect(page).toHaveURL(/\/w\/[0-9a-f-]+$/)
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })
}

test('"Go to C Note" reaches Home from any step', async ({ page }) => {
  await signup(page, undefined, { wizard: true })
  await page.getByRole('link', { name: 'Go to C Note' }).click()
  await expect(page).toHaveURL(/\/w\/[0-9a-f-]+$/)
  expect(await doneAt(page)).toBeTruthy()
})

test('a failed save still reaches Home, where a notice offers to try again', async ({ page }) => {
  await signup(page, undefined, { wizard: true })
  const block = (r: { abort: () => Promise<void> }) => r.abort()
  await page.route(/\/commands\/(workspace\.update|onboarding\.complete)$/, block)
  await page.getByRole('radio', { name: /Trading company/ }).check({ force: true })
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(alert(page)).toBeVisible() // stays on step 1 with the reason
  await expect(page.getByText('Step 1 of 3')).toBeVisible()
  await page.getByRole('button', { name: 'Skip for now' }).click()
  await expect(page).toHaveURL(/\/w\/[0-9a-f-]+$/)
  const notice = page.getByRole('status').filter({ hasText: 'Some of your setup was not saved.' })
  await expect(notice).toBeVisible()
  expect(await doneAt(page)).toBeFalsy()

  await page.unroute(/\/commands\/(workspace\.update|onboarding\.complete)$/, block)
  await notice.getByRole('button', { name: 'Try again' }).click()
  await expect(notice).toHaveCount(0)
  await expect.poll(() => doneAt(page)).toBeTruthy()
  const ws = await run<{ settings: { business_type: string } }>(page, 'workspace.get')
  expect(ws.settings.business_type).toBe('trading')
})

test('a member who opens setup lands on Home', async ({ browser }) => {
  const oc = await browser.newContext()
  const mc = await browser.newContext()
  const owner = await oc.newPage()
  const member = await mc.newPage()
  try {
    await signup(owner)
    const ws = wsOf(owner)
    const u = newUser()
    await signup(member, u)
    await run(owner, 'member.add', { email: u.email, role: 'member' })
    await member.goto(`/w/${ws}/welcome`)
    await expect(member).toHaveURL(new RegExp(`/w/${ws}$`))
    await expect(member.getByRole('heading', { name: /Welcome back/ })).toBeVisible()
  } finally {
    await oc.close()
    await mc.close()
  }
})

test.describe('the "Get set up" guide', () => {
  test.skip(!homeWidgets, 'needs HOME_WIDGETS=1')

  test('rows tick from real actions, open items first', async ({ page }) => {
    await signup(page)
    const guide = page.getByRole('region', { name: 'Get set up' })
    await expect(guide.getByText('0 of 6 done')).toBeVisible()
    // at most five rows, then "Show all"
    await expect(guide.getByRole('link')).toHaveCount(5 + 1) // five rows and "Finish setup"
    await guide.getByRole('button', { name: 'Show all (6)' }).click()
    await expect(guide.getByRole('link')).toHaveCount(6 + 1)

    const space = (await run<{ id: string }[]>(page, 'space.list'))[0].id
    await run(page, 'contact.create', { space_id: space, name: `Buyer ${uniq()}`, kind: 'company' })
    await run(page, 'company.update', { legal_name: 'Sharma Electricals', state_code: '27' })
    await page.reload()
    await expect(guide.getByText('2 of 6 done')).toBeVisible()
    await guide.getByRole('button', { name: 'Show all (6)' }).click()
    await expect(guide.getByRole('link', { name: /Done: Add your first customer/ })).toBeVisible()
    await expect(guide.getByRole('link', { name: /Done: Add your business name and state/ })).toBeVisible()
    await expect(guide.getByRole('link', { name: /To do: Send your first invoice/ })).toBeVisible()
    // open items come before done ones
    const names = await guide.getByRole('listitem').allTextContents()
    expect(names[0]).toMatch(/^To do:/)
    expect(names.at(-1)).toMatch(/^Done:/)

    await guide.getByRole('link', { name: /Send your first invoice/ }).click()
    await expect(page.getByRole('dialog', { name: 'New invoice' })).toBeVisible()
  })

  test('hide and bring back without losing a customised layout', async ({ page }) => {
    await signup(page)
    const home = `/w/${wsOf(page)}`
    const layout = [
      { id: 'ask', type: 'ask', size: 'l' },
      { id: 'links.quick', type: 'links.quick', size: 'm' },
      { id: 'setup', type: 'setup', size: 'l' },
    ]
    await run(page, 'ui_config.set', { screen: 'home', config: { v: 1, widgets: layout } })
    const saved = async () =>
      (
        await run<{ config: { widgets: { type: string }[] } }>(page, 'ui_config.get', { screen: 'home' })
      ).config.widgets.map((w) => w.type)
    await page.goto(home)
    const guide = page.getByRole('region', { name: 'Get set up' })
    await expect(guide).toBeVisible()
    await guide.getByRole('button', { name: 'Hide setup guide' }).click()
    await expect(guide).toHaveCount(0)
    await expect.poll(saved).toEqual(['ask', 'links.quick'])
    await page.reload()
    await expect(page.getByRole('region', { name: 'Quick links' })).toBeVisible()
    await expect(guide).toHaveCount(0)

    await page.getByRole('button', { name: 'User menu' }).click()
    await page.getByRole('menuitem', { name: 'Setup guide' }).click()
    await expect(guide).toBeVisible()
    await expect.poll(saved).toEqual(['setup', 'ask', 'links.quick'])
  })

  test('someone who never customised can hide it and bring it back', async ({ page }) => {
    await signup(page)
    const guide = page.getByRole('region', { name: 'Get set up' })
    await guide.getByRole('button', { name: 'Hide setup guide' }).click()
    await expect(guide).toHaveCount(0)
    await page.getByRole('button', { name: 'User menu' }).click()
    await page.getByRole('menuitem', { name: 'Setup guide' }).click()
    await expect(guide).toBeVisible()
    await page.reload()
    await expect(guide).toBeVisible()
  })
})

test('the four first-run empty states', async ({ page }) => {
  await signup(page)
  const base = `/w/${wsOf(page)}`
  await page.goto(`${base}/crm`)
  await expect(page.getByText('No customers yet')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Add customer' })).toBeVisible()
  await page.goto(`${base}/billing`)
  await expect(page.getByText('No invoices yet')).toBeVisible()
  await expect(page.getByText('Make a GST invoice in under a minute')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Import from Tally' })).toBeVisible()
  await page.goto(`${base}/tables`)
  await expect(page.getByText('No tables yet')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Start from a template' })).toBeVisible()
  await page.goto(`${base}/site`)
  await expect(page.getByText(/No website yet/)).toBeVisible()
})
