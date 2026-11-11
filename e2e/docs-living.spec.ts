import { expect, test } from '@playwright/test'
import { createSpace, signup, uniq } from './helpers'

test('verify a page, ask with citation, unanswered question becomes a capture request', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await page.getByRole('button', { name: 'New page' }).last().click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  const ws = page.url().match(/\/w\/([0-9a-f-]+)/)?.[1]

  const word = `quokka${uniq()}`
  const titleSaved = page.waitForResponse(
    (r) => r.url().includes('note.update') && !!r.request().postData()?.includes('"title"'),
  )
  await page.getByLabel('Page title').fill(`Runbook ${word}`)
  await page.getByLabel('Page title').press('Enter')
  await titleSaved

  const saved = page.waitForResponse(
    (r) => r.url().includes('note.update') && !!r.request().postData()?.includes('content_md'),
  )
  await page.getByRole('textbox', { name: 'Page content' }).click()
  await page.keyboard.type(`Deploys of ${word} are tracked in ENG-1234 and run every Friday.`)
  await saved

  await page.getByRole('button', { name: 'Verify' }).click()
  const dlg = page.getByRole('dialog', { name: 'Verify page' })
  // New pages already show "Fresh until", so wait for the verify itself before navigating away.
  const verified = page.waitForResponse((r) => r.url().includes('doc.verify') && r.ok())
  await dlg.getByRole('button', { name: 'Confirm verified' }).click()
  await verified
  await expect(page.getByText(/Fresh until/)).toBeVisible()

  await page.goto(`/w/${ws}/docs`)
  await page.getByLabel('Ask a question').fill(`How are ${word} deploys run?`)
  await page.getByRole('button', { name: 'Ask', exact: true }).click()
  const sources = page.getByRole('list', { name: 'Sources' })
  await expect(sources.getByText(`Runbook ${word}`)).toBeVisible()
  await expect(sources.getByText(/Verified \d{4}-\d{2}-\d{2}/)).toBeVisible()

  const q = `zzunmatched${uniq()}`
  await page.getByLabel('Ask a question').fill(`Who maintains ${q}?`)
  await page.getByRole('button', { name: 'Ask', exact: true }).click()
  await expect(page.getByText(/Capture request sent to/)).toBeVisible()
  await page.reload()
  await expect(page.getByRole('region', { name: 'Capture requests' }).getByText(new RegExp(q))).toBeVisible()
})
