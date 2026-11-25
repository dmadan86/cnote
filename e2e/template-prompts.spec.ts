import { expect, type Page, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

// Template prompts (docs-parity-b 5.8): {{ask.name}} becomes a text box in "Use template"; answers are plain text.

const wsOf = (page: Page) => /\/w\/([0-9a-f-]+)/.exec(page.url())![1]

async function openGallery(page: Page) {
  await page.goto(`/w/${wsOf(page)}/docs`)
  await page.getByRole('button', { name: 'New from template' }).click()
  const gallery = page.getByRole('dialog', { name: 'Template gallery' })
  await expect(gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option').first()).toBeVisible()
  return gallery
}

test('a built-in template asks for its blanks and keeps the answers as plain text', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const gallery = await openGallery(page)
  await gallery.getByRole('option', { name: /Meeting notes/ }).click()
  const fill = gallery.getByRole('group', { name: 'Fill in' })
  await expect(fill.getByLabel('Date', { exact: true })).toBeVisible()
  await expect(fill.getByLabel('Attendees')).toBeVisible()
  // templates without blanks show no such group
  await gallery.getByRole('option', { name: /Weekly team report/ }).click()
  await expect(gallery.getByRole('group', { name: 'Fill in' })).toHaveCount(0)
  await gallery.getByRole('option', { name: /Meeting notes/ }).click()

  await fill.getByLabel('Date', { exact: true }).fill('**9 Dec** [pay here](http://evil.test/x) <b>now</b>')
  await fill.getByLabel('Attendees').fill('Asha | Ravi')
  await gallery.getByRole('button', { name: 'Use template' }).click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const editor = page.getByRole('textbox', { name: 'Page content' })
  await expect(editor).toContainText('**9 Dec** [pay here](http://evil.test/x) <b>now</b>')
  await expect(editor).toContainText('Asha | Ravi')
  await expect(editor.locator('a, b, strong[data-x]')).toHaveCount(0)
  await expect(editor.locator('a[href*="evil.test"]')).toHaveCount(0)
})

test('an unanswered prompt stays in the page as its placeholder', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const gallery = await openGallery(page)
  await gallery.getByRole('option', { name: /Meeting notes/ }).click()
  await gallery.getByRole('group', { name: 'Fill in' }).getByLabel('Attendees').fill('Asha')
  await gallery.getByRole('button', { name: 'Use template' }).click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const editor = page.getByRole('textbox', { name: 'Page content' })
  await expect(editor).toContainText('{{ask.date}}')
  await expect(editor).toContainText('Asha')
})

test('a saved template with {{ask.name}} gets its own fill-in step', async ({ page }) => {
  await signup(page)
  const space = await createSpace(page)
  const ws = wsOf(page)
  const spaceId = (
    await (await page.request.post(`/api/w/${ws}/commands/space.list`, { data: {} })).json()
  ).find((s: { name: string }) => s.name === space).id
  const name = `Client brief ${uniq()}`
  const note = await (
    await page.request.post(`/api/w/${ws}/commands/note.create`, {
      data: {
        space_id: spaceId,
        title: name,
        content_md: 'Brief for {{ask.client_name}}, due {{ask.due_date}}.',
      },
    })
  ).json()
  await page.goto(`/w/${ws}/n/${note.id}`)
  // the save dialog explains the syntax
  await page.getByRole('button', { name: 'Save as template' }).click()
  const save = page.getByRole('dialog', { name: 'Save as template' })
  await expect(save).toContainText('{{ask.meeting_date}}')
  await save.getByLabel('Template name').fill(name)
  await save.getByRole('button', { name: 'Save template' }).click()
  await expect(save.getByRole('status')).toContainText('Saved template')
  await save.getByRole('button', { name: 'Done' }).click()

  const gallery = await openGallery(page)
  await gallery.getByRole('button', { name: 'Your templates' }).click()
  await gallery.getByRole('option', { name: new RegExp(name) }).click()
  const fill = gallery.getByRole('group', { name: 'Fill in' })
  await expect(fill.getByLabel('Client name')).toBeVisible()
  await expect(fill.getByLabel('Due date')).toBeVisible()
  await fill.getByLabel('Client name').fill('Asha Rao')
  await gallery.getByRole('button', { name: 'Use template' }).click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText(
    'Brief for Asha Rao, due {{ask.due_date}}.',
  )
})
