import { expect, request, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

const CHARS = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'
/** Valid 15-character GSTIN (mod-36 checksum), as in quotes.spec. */
function gstin(state: string, pan: string) {
  const body = `${state}${pan}1Z`
  const sum = [...body].reduce((s, ch, i) => {
    const v = CHARS.indexOf(ch) * (i % 2 ? 2 : 1)
    return s + Math.floor(v / 36) + (v % 36)
  }, 0)
  return body + CHARS[(36 - (sum % 36)) % 36]
}
const day = (offset: number) =>
  new Date(Date.now() + 330 * 60000 + offset * 86400000).toISOString().slice(0, 10)

test('home shows my overdue task, an unpaid invoice and an agent confirmation, and links open them', async ({
  page,
  baseURL,
}) => {
  await signup(page)
  await createSpace(page)
  const ws = new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1]
  const base = `/w/${ws}`
  const run = async <T>(name: string, input: unknown = {}) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data: input })
    expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
    return (await r.json()) as T
  }
  const me = await (await page.request.get('/api/auth/me')).json()

  await run('workspace.update', {
    settings: {
      gstin: gstin('29', 'ABCDE1234F'),
      state_code: '29',
      agent_tool_groups: ['read', 'manage_invoices'],
    },
  })
  const space = (await run<{ id: string }[]>('space.list'))[0].id
  const project = await run<{ id: string }>('space.create', {
    key: 'HOM',
    name: 'Home proj',
    template: 'software',
  })
  const title = `Overdue ${uniq()}`
  const item = await run<{ key: string }>('item.create', {
    space_id: project.id,
    title,
    assignee_id: me.user.id,
    due_date: day(-2),
  })
  const contact = await run<{ id: string }>('contact.create', {
    space_id: space,
    name: `Buyer ${uniq()}`,
    kind: 'company',
    gstin: gstin('29', 'AABCT1332L'),
  })
  const draft = async (due?: string) =>
    (
      await run<{ id: string }>('invoice.create_draft', {
        space_id: space,
        contact_id: contact.id,
        invoice_date: day(-30),
        ...(due ? { due_date: due } : {}),
        lines: [{ description: 'Work', hsn_sac: '998314', unit_price_minor: 100000, gst_rate_bp: 0 }],
      })
    ).id
  const invoice = await draft(day(-5))
  await run('invoice.issue', { id: invoice })

  // an agent asks to issue another invoice: a human has to confirm
  const agent = await run<{ api_key: string }>('agent.create', {
    name: 'Biller',
    scopes: ['read', 'manage_invoices'],
  })
  const ctx = await request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${agent.api_key}` },
  })
  const asked = await ctx.post(`/api/w/${ws}/commands/invoice.issue`, { data: { id: await draft() } })
  expect((await asked.json()).confirmation_required).toBe(true)
  await ctx.dispose()

  await page.goto(base)
  const tiles = page.getByRole('region', { name: 'Today at a glance' })
  await expect(tiles.getByRole('link', { name: /^Payments pending: ₹1,000\.00/ })).toBeVisible()
  await expect(tiles.getByRole('link', { name: /^Tasks due: 1/ })).toBeVisible()
  await expect(tiles.getByRole('link', { name: /^Approvals waiting: 1/ })).toBeVisible()
  await expect(tiles.getByRole('link', { name: /^Urgent: 2/ })).toBeVisible()
  await expect(tiles.getByRole('link', { name: /^Payments pending.*1 invoice overdue/ })).toBeVisible()

  const attention = page
    .getByRole('region', { name: 'Needs your attention' })
    .or(page.locator('section[aria-labelledby="attention"]'))
  const list = attention.first()
  await expect(list.getByRole('link', { name: new RegExp(`${title}.*Overdue by 2 days`) })).toBeVisible()
  await expect(list.getByRole('link', { name: /Invoice INV\/.*overdue by 5 days/ })).toBeVisible()
  await expect(list.getByRole('link', { name: /Approve invoice\.issue/ })).toBeVisible()

  // the links open the records
  await list.getByRole('link', { name: new RegExp(title) }).click()
  await expect(page).toHaveURL(new RegExp(`/projects/${item.key}$`))
  await page.goBack()
  await list.getByRole('link', { name: /Invoice INV\// }).click()
  await expect(page).toHaveURL(new RegExp(`/billing/${invoice}$`))
  await page.goBack()

  // tiles open the filtered lists
  await tiles.getByRole('link', { name: /^Payments pending/ }).click()
  await expect(page).toHaveURL(/\/billing\?filter=outstanding$/)
  await expect(page.getByText('Showing: unpaid invoices')).toBeVisible()
  await expect(page.getByRole('link', { name: /^INV\// })).toHaveCount(1)
  await page.goBack()
  await tiles.getByRole('link', { name: /^Tasks due/ }).click()
  await expect(
    page
      .getByRole('region', { name: 'My tasks due or overdue' })
      .getByRole('link', { name: new RegExp(title) }),
  ).toBeVisible()
  await page.goBack()
  await tiles.getByRole('link', { name: /^Approvals waiting/ }).click()
  await expect(page.getByText('invoice.issue')).toBeVisible()
})
