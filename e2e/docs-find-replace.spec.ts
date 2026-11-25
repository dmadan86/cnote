import { expect, type Locator, type Page, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Docs C1 R2 (docs-parity-c 5.5): find and replace inside the editor, as editor transactions.
const find = (bar: Locator) => bar.getByRole('textbox', { name: 'Find', exact: true })
const mod = process.platform === 'darwin' ? 'Meta' : 'Control'

async function setup(page: Page, body: string, level?: 'view') {
  await signup(page)
  const ws = page.url().split('/w/')[1]
  const call = async (name: string, data: unknown, p = page) => {
    const r = await p.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBe(true)
    return r.json()
  }
  const space = await call('space.create', {
    key: `F${uniq().slice(0, 4)}`,
    name: 'Find space',
    visibility: 'open',
  })
  const note = await call('note.create', { space_id: space.id, title: 'Find me', content_md: body })
  await page.goto(`/w/${ws}/n/${note.id}`)
  const editor = page.getByRole('textbox', { name: 'Page content' })
  await expect(editor).toContainText(body.split('\n')[0].slice(0, 8))
  void level
  return { ws, call, note, editor }
}

const BODY = [
  'The red car and the Red bus',
  '',
  'Predicted reddish tint',
  '',
  '```',
  'red in code',
  '```',
].join('\n')

test('Ctrl/Cmd+F opens the bar, highlights, counts, steps and respects match case, whole word and code', async ({
  page,
}) => {
  const { editor } = await setup(page, BODY)
  await editor.click()
  await page.keyboard.press(`${mod}+f`)
  const bar = page.getByTestId('find-bar')
  await expect(bar).toBeVisible()
  await expect(find(bar)).toBeFocused()
  await find(bar).fill('red')
  // The red, Red, Predicted, reddish; not the code block
  await expect(page.getByTestId('find-count')).toHaveText('1 of 4')
  await expect(editor.locator('.find-match')).toHaveCount(4)
  await expect(editor.locator('.find-current')).toHaveCount(1)
  await find(bar).press('Enter')
  await expect(page.getByTestId('find-count')).toHaveText('2 of 4')
  await find(bar).press('Shift+Enter')
  await expect(page.getByTestId('find-count')).toHaveText('1 of 4')
  await bar.getByLabel('Match case').check()
  await expect(page.getByTestId('find-count')).toHaveText('1 of 3')
  await bar.getByLabel('Whole word').check()
  await expect(page.getByTestId('find-count')).toHaveText('1 of 1')
  await bar.getByLabel('Include code').check()
  await expect(page.getByTestId('find-count')).toHaveText('1 of 2')
  await find(bar).fill('nothing here')
  await expect(page.getByTestId('find-count')).toHaveText('No results')
  // Esc closes, clears the highlights and returns focus to the page
  await page.keyboard.press('Escape')
  await expect(bar).toHaveCount(0)
  await expect(editor.locator('.find-match')).toHaveCount(0)
  await expect(editor).toBeFocused()
})

test('Replace and Replace all edit the page, skip code, and undo reverts a replace all in one step', async ({
  page,
}) => {
  const { editor, ws, note, call } = await setup(page, BODY)
  await editor.click()
  await page.keyboard.press(`${mod}+f`)
  const bar = page.getByTestId('find-bar')
  await find(bar).fill('red')
  await bar.getByLabel('Whole word').check()
  await bar.getByLabel('Replace with').fill('blue')
  await bar.getByRole('button', { name: 'Replace', exact: true }).click()
  await expect(editor).toContainText('The blue car and the Red bus')
  await page.waitForTimeout(700) // past the undo manager's capture window: undo then reverts only the replace all
  await bar.getByRole('button', { name: 'Replace all' }).click()
  await expect(editor).toContainText('The blue car and the blue bus')
  await expect(editor).toContainText('Predicted reddish tint') // whole word only
  await expect(editor).toContainText('red in code') // code is never touched unless chosen
  await expect
    .poll(async () => (await call('note.get', { id: note.id })).content_md)
    .toContain('The blue car and the blue bus')

  // undo reverts the last replace (one step), from the page
  await editor.click()
  await page.keyboard.press(`${mod}+z`)
  await expect(editor).toContainText('The blue car and the Red bus')
  expect(ws).toBeTruthy()
})

test('a replace in one tab shows up live in the other', async ({ page, browser }) => {
  const { editor, ws, note } = await setup(page, 'alpha beta alpha')
  const p2 = await (await browser.newContext({ storageState: await page.context().storageState() })).newPage()
  await p2.goto(`/w/${ws}/n/${note.id}`)
  const editor2 = p2.getByRole('textbox', { name: 'Page content' })
  await expect(editor2).toContainText('alpha beta alpha')

  await editor.click()
  await page.keyboard.press(`${mod}+f`)
  const bar = page.getByTestId('find-bar')
  await find(bar).fill('alpha')
  await bar.getByLabel('Replace with').fill('omega')
  await bar.getByRole('button', { name: 'Replace all' }).click()
  await expect(editor2).toContainText('omega beta omega')
  await p2.context().close()
})

test('a read-only page gets find without replace', async ({ page, browser }) => {
  const { editor, ws, note, call } = await setup(page, 'one two one')
  // archived pages are read-only
  await call('doc.archive', { id: note.id })
  await page.goto(`/w/${ws}/n/${note.id}`)
  await expect(page.getByText('Archived. This page is read-only')).toBeVisible()
  await page.getByRole('textbox', { name: 'Page content' }).click()
  await page.keyboard.press(`${mod}+f`)
  const bar = page.getByTestId('find-bar')
  await expect(bar).toBeVisible()
  await find(bar).fill('one')
  await expect(page.getByTestId('find-count')).toHaveText('1 of 2')
  await expect(bar.getByLabel('Replace with')).toHaveCount(0)
  await expect(bar.getByRole('button', { name: 'Replace all' })).toHaveCount(0)
  void editor
  void browser
})
