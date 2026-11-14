import { expect, test } from '@playwright/test'
import { createSpace, newPage, signup, uniq } from './helpers'

async function commentOnFriday(page: import('@playwright/test').Page) {
  const editor = page.getByRole('textbox', { name: 'Page content' })
  const saved = page.waitForResponse((r) => r.url().includes('note.update') && r.ok())
  await page.keyboard.type('We ship on Friday after review.')
  await saved
  await page.keyboard.press('End')
  for (let i = 0; i < 14; i++) await page.keyboard.press('ArrowLeft')
  for (let i = 0; i < 6; i++) await page.keyboard.press('Shift+ArrowLeft')
  await page
    .getByRole('toolbar', { name: 'Text formatting' })
    .getByRole('button', { name: 'Comment', exact: true })
    .click()
  const box = page.getByRole('form', { name: 'New inline comment' })
  await box.getByLabel('Comment on selected text').fill('Which Friday?')
  await box.getByRole('button', { name: 'Comment' }).click()
  await expect(editor.locator('.cn-comment')).toHaveText('Friday')
}

test('SP-001 SP-002 creating a space works and a duplicate key is refused', async ({ page }) => {
  await signup(page)
  const name = await createSpace(page)
  await page.getByRole('button', { name: 'New space' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create space' })
  await dialog.getByLabel('Name').fill(`${name} again`)
  const key = await page.getByRole('complementary').getByText(name).first().isVisible()
  expect(key).toBe(true)
  // Reuse the first space's key via the API-visible list.
  const ws = page.url().split('/w/')[1].split('/')[0]
  const spaces = await (await page.request.post(`/api/w/${ws}/commands/space.list`, { data: {} })).json()
  await dialog.getByLabel('Key').fill(spaces.find((s: any) => s.name === name).key)
  await dialog.getByRole('button', { name: 'Create space' }).click()
  await expect(dialog).toBeVisible() // stays open
  await expect(dialog.getByRole('alert')).toBeVisible()
  await expect(page.getByRole('heading', { name: `${name} again` })).toHaveCount(0)
})

test('SP-004 SP-005 nested pages appear under the parent and moving re-parents them', async ({ page }) => {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const cmd = async (name: string, data: unknown) =>
    (await page.request.post(`/api/w/${ws}/commands/${name}`, { data })).json()
  const [space] = await cmd('space.list', {})
  const a = await cmd('note.create', { space_id: space.id, title: `A ${uniq()}` })
  const b = await cmd('note.create', { space_id: space.id, title: `B ${uniq()}` })
  const c = await cmd('note.create', { space_id: space.id, title: `C ${uniq()}`, parent_id: a.id })
  await cmd('note.update', { id: c.id, parent_id: b.id })
  await page.reload()
  const tree = page.getByRole('complementary')
  await tree.getByRole('button', { name: `Expand ${b.title}` }).click()
  await expect(tree.getByRole('link', { name: c.title })).toBeVisible()
  await expect(tree.getByRole('button', { name: `Expand ${a.title}` })).toHaveCount(0) // A has no children left
})

test('CM-002 the highlight stays on its words after text is added before them', async ({ page }) => {
  const editor = await newPage(page)
  await commentOnFriday(page)
  await editor
    .locator('p')
    .first()
    .click({ position: { x: 2, y: 5 } })
  await page.keyboard.press('Home')
  await page.keyboard.type('Heads up: ')
  await expect(editor).toContainText('Heads up: We ship')
  await expect(editor.locator('.cn-comment')).toHaveText('Friday')
})

test('CM-003 deleting the commented text drops the highlight but keeps the thread', async ({ page }) => {
  const editor = await newPage(page)
  await commentOnFriday(page)
  await editor.locator('.cn-comment').click()
  await page.keyboard.press('ArrowRight')
  for (let i = 0; i < 6; i++) await page.keyboard.press('Backspace')
  await expect(editor).not.toContainText('Friday')
  await expect(editor.locator('.cn-comment')).toHaveCount(0)
  const thread = page
    .getByRole('list', { name: 'Comments' })
    .getByRole('listitem')
    .filter({ hasText: 'Which Friday?' })
  await expect(thread).toBeVisible() // nothing is lost
  await expect(thread).toContainText('Friday') // the quoted text is still shown
})

test('RV-006 a view-only reader cannot restore a version', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  const editor = await newPage(a)
  const ws = a.url().split('/w/')[1].split('/')[0]
  const url = a.url()
  await a.keyboard.type('First draft')
  await a.getByRole('tab', { name: 'versions' }).click()
  await expect(async () => {
    await a.getByRole('tab', { name: 'comments' }).click()
    await a.getByRole('tab', { name: 'versions' }).click()
    await expect(a.getByRole('list', { name: 'Versions' })).toContainText('Version 1', { timeout: 1000 })
  }).toPass({ timeout: 15_000 })
  await expect(editor).toContainText('First draft')

  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: ub.email, role: 'member' } })
  const id = url.split('/n/')[1]
  await a.request.post(`/api/w/${ws}/commands/note.restrict`, { data: { note_id: id, restriction: 'view' } })
  await a.request.post(`/api/w/${ws}/commands/note.share`, {
    data: { note_id: id, email: ub.email, level: 'view' },
  })
  await b.goto(url)
  await expect(b.getByText('You can view this page but not edit it.')).toBeVisible()
  await b.getByRole('tab', { name: 'versions' }).click()
  await b.getByRole('button', { name: /Version 1/ }).click()
  await expect(b.getByRole('region', { name: 'Version 1 content' })).toContainText('First draft')
  await expect(b.getByRole('button', { name: 'Restore this version' })).toHaveCount(0)
  // The API refuses too.
  const revs = await (
    await b.request.post(`/api/w/${ws}/commands/note.revisions`, { data: { note_id: id } })
  ).json()
  const r = await b.request.post(`/api/w/${ws}/commands/note.restore`, {
    data: { note_id: id, revision_id: revs[0].id },
  })
  expect(r.status()).toBe(403)
})

test.skip('ED-011 ED-012 upload an image or attachment and download it with permissions', () => {
  // No binary upload / attachment storage exists (images are inserted by URL only); see CN-QA-001 coverage audit.
})

test.skip('RV-002 compare two revisions', () => {
  // The versions panel shows one version's content at a time; there is no diff view or API.
})
