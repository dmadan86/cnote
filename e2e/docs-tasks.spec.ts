import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

// docs-parity-b 4.4: assign a to-do with a due date, the assignee sees it under Today and opens the page at the task.

const iso = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

test.setTimeout(90_000)

test("a task assigned with a due date shows up in the assignee's My tasks and opens at the task", async ({
  browser,
}) => {
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
  await a.getByLabel('Page title').fill('Month end')
  await a.getByLabel('Page title').blur()

  // A to-do item, then the two buttons under it: Assign (the "@" picker) and Due (the date picker).
  const editor = a.getByRole('textbox', { name: 'Page content' })
  await editor.click()
  await a.keyboard.type('Intro line')
  await a.keyboard.press('Enter')
  await a.keyboard.type('/')
  await a
    .getByRole('listbox', { name: 'Insert block' })
    .getByRole('option', { name: /To-do list/ })
    .click()
  await a.keyboard.type('Send the GST summary')
  const task = editor.locator('ul[data-type="taskList"] li').first()
  await expect(task).toContainText('Send the GST summary')

  const options = a.getByRole('group', { name: 'Task options' })
  await options.getByRole('button', { name: 'Assign task' }).click()
  const picker = a.getByRole('listbox', { name: 'Mention someone' })
  await picker.getByRole('option', { name: new RegExp(ub.name) }).click()
  await expect(options.getByRole('button', { name: new RegExp(`Assigned to @?${ub.name}`) })).toBeVisible()

  const today = iso(new Date())
  await options.getByRole('button', { name: 'Set due date' }).click()
  await options.getByLabel('Due date').fill(today)
  await expect(options.getByRole('button', { name: /^Due \d+ \w+ \d{4}, change$/ })).toBeVisible()

  const saved = a.waitForResponse(
    (r) =>
      r.url().includes('/commands/note.update') &&
      (r.request().postData() ?? '').includes(`datetime=\\"${today}\\"`),
  )
  await a.keyboard.type(' ')
  await saved.catch(() => {}) // live editing saves through the gateway instead

  // The assignee: My tasks, Today group, the row opens the page at the task.
  await b.goto(`/w/${ws}/tasks`)
  await expect(b.getByRole('heading', { name: 'My tasks' })).toBeVisible()
  const today_ = b.getByRole('region', { name: /^Today/ })
  const row = today_.getByRole('link', { name: /Send the GST summary/ })
  // The index is built after live editing saves the page, and the list loads once: reload until the task is in it.
  await expect(async () => {
    await b.reload()
    await expect(row).toBeVisible({ timeout: 2_000 })
  }).toPass({ timeout: 45_000 })
  await expect(row).toContainText('Month end')
  await expect(b.getByText('1 open')).toBeVisible()
  await row.click()
  await expect(b).toHaveURL(new RegExp(`${noteUrl.split('/w/')[1]}#task-0$`))
  await expect(
    b.getByRole('textbox', { name: 'Page content' }).locator('ul[data-type="taskList"] li').first(),
  ).toContainText('Send the GST summary')
})
