import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

test('edit, react to and delete a comment; a reply keeps the thread', async ({ browser }) => {
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
  await a.getByLabel('Page title').fill('Comment plan')
  await a.getByLabel('Page title').blur()
  const add = (body: string, extra = {}) =>
    a.request.post(`/api/w/${ws}/commands/comment.add`, { data: { note_id: noteId, body, ...extra } })
  const top = (await (await add('First thought')).json()).id as string
  await add('A reply', { parent_id: top })

  // A edits her own comment: it shows "edited"
  await a.goto(noteUrl)
  const list = a.getByRole('list', { name: 'Comments' })
  await list.getByRole('button', { name: 'Comment actions' }).first().click()
  await list.getByRole('button', { name: 'Edit', exact: true }).click()
  await a.getByLabel('Edit comment text').fill('Second thought')
  await a.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(list).toContainText('Second thought')
  await expect(list).toContainText('edited')

  // A member who did not write it has no Edit or Delete
  await b.goto(noteUrl)
  const lb = b.getByRole('list', { name: 'Comments' })
  await expect(lb).toContainText('Second thought')
  await expect(lb.getByRole('button', { name: 'Comment actions' })).toHaveCount(0)

  // B reacts with the keyboard-reachable picker; the chip counts and A sees it
  await lb.getByRole('button', { name: 'Add reaction' }).first().click()
  await b.getByRole('button', { name: 'React with 👍' }).click()
  const chip = lb.getByRole('button', { name: /^👍 1, you reacted/ })
  await expect(chip).toHaveAttribute('aria-pressed', 'true')
  await chip.focus()
  await expect(lb.getByRole('tooltip')).toContainText('You reacted with 👍')
  await a.reload()
  await expect(list.getByRole('button', { name: '👍 1' })).toHaveAttribute('aria-pressed', 'false')
  await list.getByRole('button', { name: '👍 1' }).focus()
  await expect(list.getByRole('tooltip')).toContainText('1 person reacted with 👍')
  // a second tap of the chip removes it
  await chip.click()
  await expect(lb.getByRole('button', { name: /^👍/ })).toHaveCount(0)

  // A deletes the top comment: the reply stays under "Comment deleted"
  a.once('dialog', (d) => d.accept())
  await list.getByRole('button', { name: 'Comment actions' }).first().click()
  await list.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(list).toContainText('Comment deleted')
  await expect(list).toContainText('A reply')
  await expect(list).not.toContainText('Second thought')
})
