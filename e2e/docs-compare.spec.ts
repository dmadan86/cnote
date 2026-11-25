import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('compare a version with the current page and two versions; changes are marked for assistive tech', async ({
  page: a,
}) => {
  await signup(a)
  await createSpace(a)
  await a.getByRole('button', { name: 'New page' }).last().click()
  await expect(a).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(a.getByText(/· live/)).toBeVisible()
  const editor = a.locator('.ProseMirror')
  await editor.click()
  await a.keyboard.type('First draft')
  await a.getByRole('tab', { name: 'versions' }).click()
  await expect(async () => {
    await a.getByRole('tab', { name: 'comments' }).click()
    await a.getByRole('tab', { name: 'versions' }).click()
    await expect(a.getByRole('list', { name: 'Versions' })).toContainText('Version 1', { timeout: 1000 })
  }).toPass({ timeout: 15_000 })
  await editor.click()
  await a.keyboard.press('End')
  await a.keyboard.type(' plus a bad edit')
  await expect(editor).toContainText('plus a bad edit')

  // One version against the current page: the added words carry text for screen readers, not only a colour
  await a.getByRole('checkbox', { name: 'Select version 1 to compare' }).check()
  const diff = a.getByRole('region', { name: 'Changes from version 1 to the current page' })
  // "Current page" is the last saved text (a 700 ms debounce after typing): compare again until it has caught up
  await expect(async () => {
    await a.getByRole('button', { name: 'Compare with current page' }).click()
    await expect(diff.getByRole('status')).toContainText('1 addition and 0 removals', { timeout: 1000 })
  }).toPass({ timeout: 15_000 })
  const ins = diff.locator('ins')
  await expect(ins).toContainText('plus a bad edit')
  await expect(ins.locator('.sr-only').first()).toHaveText('Added:')
  await expect(ins).toHaveCSS('text-decoration-line', 'underline')

  // Restore, then compare the two versions: the removal is marked and struck through
  await a.getByRole('button', { name: /Version 1/ }).click()
  await a.getByRole('button', { name: 'Restore this version' }).click()
  await a.getByRole('button', { name: 'Restore version 1' }).click()
  await expect(editor).not.toContainText('bad edit')
  const list = a.getByRole('list', { name: 'Versions' })
  await expect(list).toContainText('Kept before a restore')
  await a.getByRole('checkbox', { name: 'Select version 1 to compare' }).check()
  await a.getByRole('checkbox', { name: /Select version 2 to compare/ }).check()
  await a.getByRole('button', { name: 'Compare selected versions' }).click()
  const two = a.getByRole('region', { name: /Changes from version 1 to version 2/ })
  await expect(two.locator('ins')).toContainText('plus a bad edit')
  await expect(two.locator('ins .sr-only').first()).toHaveText('Added:')
  // and back the other way against the page as restored: the bad edit is gone
  await a.getByRole('checkbox', { name: 'Select version 2 to compare' }).uncheck()
  await a.getByRole('button', { name: 'Compare with current page' }).click()
  await expect(
    a.getByRole('region', { name: 'Changes from version 1 to the current page' }).getByRole('status'),
  ).toContainText('No differences')
})

test('a reader of a page with edit restricted can compare but not restore', async ({ browser }) => {
  const a = await (await browser.newContext()).newPage()
  await signup(a)
  const ws = a.url().split('/w/')[1]
  const b = await (await browser.newContext()).newPage()
  const ub = await signup(b)
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/member.add`, { data: { email: ub.email, role: 'member' } })
    ).ok(),
  ).toBe(true)
  await createSpace(a)
  await a.getByRole('button', { name: 'New page' }).last().click()
  await expect(a).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const noteUrl = a.url()
  const noteId = noteUrl.split('/n/')[1]
  await expect(a.getByText(/· live/)).toBeVisible()
  await a.locator('.ProseMirror').click()
  await a.keyboard.type('Original text')
  await a.getByRole('tab', { name: 'versions' }).click()
  await expect(async () => {
    await a.getByRole('tab', { name: 'comments' }).click()
    await a.getByRole('tab', { name: 'versions' }).click()
    await expect(a.getByRole('list', { name: 'Versions' })).toContainText('Version 1', { timeout: 1000 })
  }).toPass({ timeout: 15_000 })
  expect(
    (
      await a.request.post(`/api/w/${ws}/commands/note.restrict`, {
        data: { note_id: noteId, restriction: 'edit' },
      })
    ).ok(),
  ).toBe(true)

  await b.goto(noteUrl)
  await b.getByRole('tab', { name: 'versions' }).click()
  await b.getByRole('checkbox', { name: 'Select version 1 to compare' }).check()
  await b.getByRole('button', { name: 'Compare with current page' }).click()
  await expect(b.getByRole('region', { name: 'Changes from version 1 to the current page' })).toBeVisible()
  await b.getByRole('button', { name: /Version 1/ }).click()
  await expect(b.getByRole('button', { name: 'Restore this version' })).toHaveCount(0)
})
