import { expect, test } from '@playwright/test'
import { markdown, newPage } from './helpers'

// docs-parity A1: the blocks render with accessible names and round-trip through the editor's Markdown.
const MD = [
  '> [!WARNING]',
  '> Keep the **vault** key safe.',
  '',
  '<details><summary>Returns policy</summary>',
  '',
  'Seven days.',
  '',
  '</details>',
  '',
  'Order <span data-status="green">DONE</span> due <time datetime="2026-12-01">1 Dec 2026</time>.',
  '',
  '[[toc]]',
  '',
  '# Policy',
  '',
  '```ts',
  'const a = 1',
  '```',
].join('\n')

test('panel, expand, status, date, table of contents and code language render and round-trip', async ({
  page,
}) => {
  const editor = await newPage(page)
  await page.evaluate(
    (md) =>
      (document.querySelector('.ProseMirror') as any).editor.commands.setContent(md, {
        contentType: 'markdown',
      }),
    MD,
  )

  await expect(editor.getByRole('note', { name: 'Warning panel' })).toContainText('Keep the vault key safe.')
  await expect(editor.getByRole('textbox', { name: 'Expand title' })).toHaveValue('Returns policy')
  await expect(editor.locator('span.cn-status[data-status="green"]')).toHaveText('DONE')
  await expect(editor.locator('time.cn-date')).toHaveAttribute('datetime', '2026-12-01')
  await expect(editor.getByRole('navigation', { name: 'Table of contents' })).toContainText('Policy')
  await expect(editor.getByRole('combobox', { name: 'Code language' })).toHaveValue('ts')

  // The expand toggles from the keyboard, and the panel kind can be changed.
  const toggle = editor.getByRole('button', { name: 'Expand or collapse' })
  await toggle.focus()
  await page.keyboard.press('Enter')
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await editor.getByRole('combobox', { name: 'Panel type' }).selectOption('error')
  await expect(editor.getByRole('note', { name: 'Error panel' })).toBeVisible()

  const out = await markdown(page)
  expect(out).toContain('> [!ERROR]')
  expect(out).toContain('<details><summary>Returns policy</summary>')
  expect(out).toContain('<span data-status="green">DONE</span>')
  expect(out).toContain('<time datetime="2026-12-01">1 Dec 2026</time>')
  expect(out).toContain('[[toc]]')
  expect(out).toContain('```ts\nconst a = 1\n```')
})
