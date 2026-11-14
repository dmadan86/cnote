import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('restore an earlier version: history is kept and an open editor reloads onto it', async ({
  browser,
}) => {
  const ctx = await browser.newContext()
  const a = await ctx.newPage()
  await signup(a)
  await createSpace(a)
  await a.getByRole('button', { name: 'New page' }).last().click()
  await expect(a).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(a.getByText(/· live/)).toBeVisible()
  const editor = a.locator('.ProseMirror')
  await editor.click()
  await a.keyboard.type('First draft')
  // The first save of a page becomes Version 1.
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

  // A second tab on the same page: it must reload onto the restored text.
  const b = await ctx.newPage()
  await b.goto(a.url())
  await expect(b.locator('.ProseMirror')).toContainText('plus a bad edit')

  await a.getByRole('button', { name: /Version 1/ }).click()
  await expect(a.getByRole('region', { name: 'Version 1 content' })).toContainText('First draft')
  await a.getByRole('button', { name: 'Restore this version' }).click()
  await a.getByRole('button', { name: 'Restore version 1' }).click()

  await expect(a.locator('.ProseMirror')).not.toContainText('bad edit')
  await expect(a.locator('.ProseMirror')).toContainText('First draft')
  await expect(b.getByText('This page was restored to an earlier version.')).toBeVisible({ timeout: 15_000 })
  await expect(b.locator('.ProseMirror')).not.toContainText('bad edit')

  // Nothing was lost: the replaced text is a version, and the restore is one too.
  const list = a.getByRole('list', { name: 'Versions' })
  await expect(list).toContainText('Kept before a restore')
  await expect(list).toContainText('Restored')
  await a.getByRole('button', { name: /Kept before a restore/ }).click()
  await expect(a.getByRole('region', { name: /content/ })).toContainText('plus a bad edit')

  // Still live after the restore: edits keep flowing between the tabs.
  await a.locator('.ProseMirror').click()
  await a.keyboard.press('End')
  await a.keyboard.type(' again')
  await expect(b.locator('.ProseMirror')).toContainText('First draft again')
})
