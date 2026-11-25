import { expect, test } from '@playwright/test'
import { createSpace, markdown, signup, uniq } from './helpers'

const LINK = /\[Quokka plan\]\(\/w\/[0-9a-f-]{36}\/n\/[0-9a-f-]{36}\)/

async function setup(browser: import('@playwright/test').Browser) {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  const cmd = async (p: typeof a, name: string, data: unknown) =>
    (await p.request.post(`/api/w/${ws}/commands/${name}`, { data })).json()
  await createSpace(a)
  const [space] = await cmd(a, 'space.list', {})
  const mk = (title: string) => cmd(a, 'note.create', { space_id: space.id, title })
  return { a, ws, cmd, mk }
}

test('[[ searches page titles and inserts an ordinary link; Escape and a code block leave the text', async ({
  browser,
}) => {
  const { a, ws, mk } = await setup(browser)
  const target = await mk('Quokka plan')
  const host = await mk(`Host ${uniq()}`)
  await a.goto(`/w/${ws}/n/${host.id}`)
  const editor = a.getByRole('textbox', { name: 'Page content' })
  await editor.click()

  await a.keyboard.type('See [[Quokka')
  await a.getByRole('option', { name: 'Quokka plan' }).click()
  await expect.poll(() => markdown(a)).toMatch(LINK)
  expect(await markdown(a)).toContain(`/w/${ws}/n/${target.id}`)

  // Escape leaves what was typed, with no link.
  await a.keyboard.type(' and [[nothing')
  await a.keyboard.press('Escape')
  await expect(a.getByRole('listbox', { name: 'Link to a page' })).toHaveCount(0)
  await expect.poll(() => markdown(a)).toContain('[[nothing')

  // Inside a code block the marker does nothing.
  await a.keyboard.press('Enter')
  await a.keyboard.type('``` ')
  await a.keyboard.type('[[Quokka')
  await expect(a.getByRole('listbox', { name: 'Link to a page' })).toHaveCount(0)
})

test('the link dialog takes a pasted URL unchanged, and also searches pages', async ({ browser }) => {
  const { a, ws, mk } = await setup(browser)
  await mk('Quokka plan')
  const host = await mk(`Host ${uniq()}`)
  await a.goto(`/w/${ws}/n/${host.id}`)
  const editor = a.getByRole('textbox', { name: 'Page content' })
  await editor.click()
  await a.keyboard.type('site')
  const bar = a.getByRole('toolbar', { name: 'Editor toolbar' })

  for (let i = 0; i < 4; i++) await a.keyboard.press('Shift+ArrowLeft')
  await bar.getByRole('button', { name: 'Link' }).click()
  const dialog = a.getByRole('dialog', { name: 'Link' })
  await dialog.getByLabel('Paste a URL or search for a page').fill('https://example.com/a?b=1&c=2')
  await dialog.getByRole('button', { name: 'Apply' }).click()
  await expect.poll(() => markdown(a)).toContain('[site](https://example.com/a?b=1&c=2)')

  await a.keyboard.press('ArrowRight')
  await a.keyboard.press('Enter')
  await a.keyboard.type('page')
  for (let i = 0; i < 4; i++) await a.keyboard.press('Shift+ArrowLeft')
  await bar.getByRole('button', { name: 'Link' }).click()
  await dialog.getByLabel('Paste a URL or search for a page').fill('Quokka')
  await dialog.getByRole('option', { name: 'Quokka plan' }).click()
  await expect.poll(() => markdown(a)).toMatch(/\[page\]\(\/w\/[0-9a-f-]{36}\/n\/[0-9a-f-]{36}\)/)
})

test('the picker never offers a restricted page to someone who cannot read it', async ({ browser }) => {
  const { a, ws, cmd, mk } = await setup(browser)
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: ub.email, role: 'member' } })
  await mk('Quokka open')
  const secret = await mk('Quokka secret')
  await cmd(a, 'note.restrict', { note_id: secret.id, restriction: 'view' })
  const host = await mk(`Host ${uniq()}`)

  await b.goto(`/w/${ws}/n/${host.id}`)
  await b.getByRole('textbox', { name: 'Page content' }).click()
  await b.keyboard.type('[[Quokka')
  await expect(b.getByRole('option', { name: 'Quokka open' })).toBeVisible()
  await expect(b.getByRole('option', { name: 'Quokka secret' })).toHaveCount(0)
})
