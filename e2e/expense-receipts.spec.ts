import { expect, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

// 1x1 PNG
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
  'base64',
)

test('Member attaches a receipt photo to a claim; it locks on submit and the owner can see it', async ({
  browser,
}) => {
  const oc = await browser.newContext()
  const mc = await browser.newContext()
  const owner = await oc.newPage()
  const member = await mc.newPage()
  try {
    await signup(owner)
    await createSpace(owner)
    const ws = new URL(owner.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
    const mu = await signup(member)
    const r = await owner.request.post(`/api/w/${ws}/commands/member.add`, {
      data: { email: mu.email, role: 'member' },
    })
    expect(r.ok(), await r.text()).toBeTruthy()

    await member.goto(`/w/${ws}/billing/expenses`)
    await member.getByRole('button', { name: 'New claim' }).click()
    const form = member.getByRole('dialog', { name: 'New expense claim' })
    await form.getByLabel('Amount paid (INR, GST included)').fill('450')
    await form.getByRole('button', { name: 'Save draft' }).click()
    const detail = member.getByRole('dialog', { name: /^Expense: / })
    const receipts = detail.getByRole('region', { name: 'Receipts' })
    await expect(receipts.getByText('No receipt attached yet.')).toBeVisible()

    // the browser's own type is ignored: a text file is refused, a real image is kept
    const input = receipts.locator('input[type=file]')
    await input.setInputFiles({
      name: 'fake.png',
      mimeType: 'image/png',
      buffer: Buffer.from('not an image'),
    })
    await expect(receipts.getByRole('alert')).toContainText('only images')
    await input.setInputFiles({ name: 'taxi.png', mimeType: 'image/png', buffer: PNG })
    await expect(receipts.getByRole('link', { name: /taxi\.png/ })).toBeVisible()
    await expect(receipts.getByRole('button', { name: 'Remove taxi.png' })).toBeVisible()

    await detail.getByRole('button', { name: 'Submit claim' }).click()
    await expect(detail.getByText('Waiting for approval')).toBeVisible()
    await expect(receipts.getByRole('link', { name: /taxi\.png/ })).toBeVisible() // still viewable
    await expect(receipts.getByRole('button', { name: 'Remove taxi.png' })).toHaveCount(0) // but locked
    await expect(receipts.getByText('Add receipt photo')).toHaveCount(0)

    // the reviewer sees the same receipt
    await owner.goto(`/w/${ws}/billing/expenses`)
    await owner.getByRole('tab', { name: /To approve/ }).click()
    await owner.getByRole('button', { name: /Travel/ }).click()
    const href = await owner
      .getByRole('region', { name: 'Receipts' })
      .getByRole('link', { name: /taxi\.png/ })
      .getAttribute('href')
    const img = await owner.request.get(href as string)
    expect(img.headers()['content-type']).toBe('image/png')
  } finally {
    await oc.close()
    await mc.close()
  }
})
