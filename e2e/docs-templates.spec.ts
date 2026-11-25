import { expect, type Page, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

// Template gallery (docs-parity 5.5, 7.6, 7.8 W4). Runs after W1: inserting panels and status needs the new blocks.

const wsOf = (page: Page) => /\/w\/([0-9a-f-]+)/.exec(page.url())![1]

async function openGallery(page: Page) {
  await page.goto(`/w/${wsOf(page)}/docs`)
  await page.getByRole('button', { name: 'New from template' }).click()
  const gallery = page.getByRole('dialog', { name: 'Template gallery' })
  await expect(gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option')).toHaveCount(12)
  return gallery
}

test('gallery: categories, search, preview, keyboard and Use template', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const gallery = await openGallery(page)

  // categories
  await gallery.getByRole('button', { name: 'HR' }).click()
  await expect(gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option')).toHaveCount(2)
  await gallery.getByRole('button', { name: 'All' }).click()

  // search
  const search = gallery.getByRole('searchbox', { name: 'Search templates' })
  await search.fill('vendor')
  await expect(gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option')).toHaveCount(1)
  await expect(
    gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option', { name: /Vendor evaluation/ }),
  ).toBeVisible()
  await search.fill('zzzz-no-such-template')
  await expect(gallery.getByText('No template matches.')).toBeVisible()
  await search.fill('')
  await expect(gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option')).toHaveCount(12)

  // preview is a labelled region and is announced through a status line
  const first = gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option').first()
  await first.focus()
  await expect(first).toHaveAttribute('aria-selected', 'true')
  await expect(gallery.getByRole('status').filter({ hasText: /^Previewing / })).toContainText(
    'Meeting notes (MoM)',
  )
  await expect(gallery.getByRole('region', { name: 'Preview of Meeting notes (MoM)' })).toContainText(
    'Attendees',
  )

  // the arrow keys move the selection, and the preview follows
  await page.keyboard.press('ArrowRight')
  const second = gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option').nth(1)
  await expect(second).toBeFocused()
  await expect(second).toHaveAttribute('aria-selected', 'true')
  await expect(first).toHaveAttribute('aria-selected', 'false')
  const name = (await second.locator('span').first().textContent()) ?? ''
  await expect(gallery.getByRole('region', { name: `Preview of ${name}` })).toBeVisible()
  await page.keyboard.press('End')
  await expect(gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option').last()).toBeFocused()
  await page.keyboard.press('Home')
  await expect(first).toBeFocused()

  // Use template creates the page and opens it
  await gallery.getByLabel('Title (optional)').fill(`Weekly sync ${uniq()}`)
  await gallery.getByRole('button', { name: 'Use template' }).click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText('Attendees')
  await expect(page.getByLabel('Page title')).toHaveValue(/Weekly sync/)
})

test('gallery: your templates sit beside the built-ins', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const ws = wsOf(page)
  const spaces = await (await page.request.post(`/api/w/${ws}/commands/space.list`, { data: {} })).json()
  const note = await (
    await page.request.post(`/api/w/${ws}/commands/note.create`, {
      data: { space_id: spaces[0].id, title: 'Godown audit', content_md: 'Count stock on {{today}}.' },
    })
  ).json()
  const name = `Godown audit ${uniq()}`
  const saved = await page.request.post(`/api/w/${ws}/commands/template.save`, {
    data: { note_id: note.id, name, source_kind: 'none' },
  })
  expect(saved.ok()).toBeTruthy()

  await page.goto(`/w/${ws}/docs`)
  await page.getByRole('button', { name: 'New from template' }).click()
  const gallery = page.getByRole('dialog', { name: 'Template gallery' })
  await expect(gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option')).toHaveCount(13)
  await gallery.getByRole('button', { name: 'Your templates' }).click()
  await expect(gallery.getByRole('listbox', { name: 'Templates' }).getByRole('option')).toHaveCount(1)
  await gallery
    .getByRole('listbox', { name: 'Templates' })
    .getByRole('option', { name: new RegExp(name) })
    .click()
  await gallery.getByRole('button', { name: 'Use template' }).click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText('Count stock on')
})

test('gallery: works on a phone-sized screen', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.setViewportSize({ width: 390, height: 780 })
  const gallery = await openGallery(page)
  await gallery
    .getByRole('listbox', { name: 'Templates' })
    .getByRole('option', { name: /Standard operating procedure/ })
    .click()
  await expect(gallery.getByRole('region', { name: /Preview of Standard operating procedure/ })).toBeVisible()
  const btn = gallery.getByRole('button', { name: 'Use template' })
  await btn.scrollIntoViewIfNeeded()
  await expect(btn).toBeInViewport()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
  expect(overflow).toBe(false)
  await btn.click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
})

test('gallery: refuses a space the person cannot create in, and an unknown built-in', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  const ws = wsOf(page)
  const spaces = await (await page.request.post(`/api/w/${ws}/commands/space.list`, { data: {} })).json()
  const bad = await page.request.post(`/api/w/${ws}/commands/template.render`, {
    data: { template_id: 'builtin:no-such-template', space_id: spaces[0].id },
  })
  expect(bad.status()).toBe(404)
  const ok = await page.request.post(`/api/w/${ws}/commands/template.render`, {
    data: { template_id: 'builtin:decision-record', space_id: spaces[0].id },
  })
  expect(ok.ok()).toBeTruthy()
})
