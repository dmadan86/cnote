import { crc32 } from 'node:zlib'
import { expect, type Page, test } from '@playwright/test'
import { createSpace, signup } from './helpers'

// Word import (docs-parity-b 5.7): one .docx becomes one page. The files are built here, so no binary is committed.

const wsOf = (page: Page) => /\/w\/([0-9a-f-]+)/.exec(page.url())![1]

/** A zip of stored (uncompressed) entries: enough for a Word package. */
function zip(files: Record<string, string | Buffer>): Buffer {
  const parts: Buffer[] = []
  const central: Buffer[] = []
  let offset = 0
  for (const [name, data] of Object.entries(files)) {
    const body = Buffer.isBuffer(data) ? data : Buffer.from(data)
    const n = Buffer.from(name)
    const h = Buffer.alloc(30)
    h.writeUInt32LE(0x04034b50, 0)
    h.writeUInt16LE(20, 4)
    h.writeUInt16LE(0x21, 12) // 1980-01-01
    h.writeUInt32LE(crc32(body), 14)
    h.writeUInt32LE(body.length, 18)
    h.writeUInt32LE(body.length, 22)
    h.writeUInt16LE(n.length, 26)
    const c = Buffer.alloc(46)
    c.writeUInt32LE(0x02014b50, 0)
    c.writeUInt16LE(20, 4)
    c.writeUInt16LE(20, 6)
    c.writeUInt16LE(0x21, 14)
    c.writeUInt32LE(crc32(body), 16)
    c.writeUInt32LE(body.length, 20)
    c.writeUInt32LE(body.length, 24)
    c.writeUInt16LE(n.length, 28)
    c.writeUInt32LE(offset, 42)
    parts.push(h, n, body)
    central.push(c, n)
    offset += 30 + n.length + body.length
  }
  const dir = Buffer.concat(central)
  const end = Buffer.alloc(22)
  end.writeUInt32LE(0x06054b50, 0)
  end.writeUInt16LE(Object.keys(files).length, 8)
  end.writeUInt16LE(Object.keys(files).length, 10)
  end.writeUInt32LE(dir.length, 12)
  end.writeUInt32LE(offset, 16)
  return Buffer.concat([...parts, dir, end])
}

const MAIN = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml'
const NS =
  'xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"'
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
)

function docx(body: string, extra: Record<string, string | Buffer> = {}, rels = '', type = MAIN) {
  return zip({
    '[Content_Types].xml': `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="${type}"/></Types>`,
    'word/document.xml': `<w:document ${NS}><w:body>${body}</w:body></w:document>`,
    'word/_rels/document.xml.rels': `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">${rels}</Relationships>`,
    ...extra,
  })
}
const p = (t: string, style = '') =>
  `<w:p>${style ? `<w:pPr><w:pStyle w:val="${style}"/></w:pPr>` : ''}<w:r><w:t>${t}</w:t></w:r></w:p>`
const REL = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'

const report = docx(
  p('Quarterly report', 'Title') +
    p('Summary', 'Heading2') +
    '<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>Revenue grew</w:t></w:r><w:r><w:t xml:space="preserve"> in </w:t></w:r><w:hyperlink r:id="l"><w:r><w:t>the north</w:t></w:r></w:hyperlink></w:p>' +
    '<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>First point</w:t></w:r></w:p>' +
    '<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Region</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Sales</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>North</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>42</w:t></w:r></w:p></w:tc></w:tr></w:tbl>' +
    '<w:p><w:r><w:drawing><a:blip r:embed="img"/></w:drawing></w:r></w:p>',
  { 'word/media/chart.png': PNG },
  `<Relationship Id="l" Type="${REL}/hyperlink" Target="https://example.com/north" TargetMode="External"/><Relationship Id="img" Type="${REL}/image" Target="media/chart.png"/>`,
)

const choose = (page: Page, name: string, buffer: Buffer) =>
  page.getByLabel('Word file').setInputFiles({
    name,
    mimeType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    buffer,
  })

test('a Word file becomes a page from the space page', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await choose(page, 'report.docx', report)
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(page.getByLabel('Page title')).toHaveValue('Quarterly report')
  const editor = page.getByRole('textbox', { name: 'Page content' })
  await expect(editor.getByRole('heading', { name: 'Summary' })).toBeVisible()
  await expect(editor.locator('strong')).toHaveText('Revenue grew')
  await expect(editor.getByRole('link', { name: 'the north' })).toHaveAttribute(
    'href',
    'https://example.com/north',
  )
  await expect(editor.getByRole('listitem')).toContainText('First point')
  await expect(editor.getByRole('cell', { name: '42' })).toBeVisible()
  // the picture went through the files path: it is served from the page's own files
  await expect(editor.locator(`img[src*="/api/w/${wsOf(page)}/files/"]`)).toBeVisible()
})

test('what was dropped is listed once, and the page opens from the notice', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  await choose(
    page,
    'notes.docx',
    docx(p('Has a footnote'), {
      'word/footnotes.xml': '<w:footnotes/>',
      'word/comments.xml': '<w:comments/>',
    }),
  )
  const notice = page.getByRole('status').filter({ hasText: 'Some things were left out' })
  await expect(notice).toContainText('footnotes were dropped')
  await expect(notice).toContainText('comments were dropped')
  await expect(page).not.toHaveURL(/\/n\//)
  await notice.getByRole('button', { name: 'Dismiss' }).click()
  await expect(notice).toBeHidden()
  await choose(page, 'notes.docx', docx(p('Again'), { 'word/footnotes.xml': '<w:footnotes/>' }))
  await page.getByRole('button', { name: 'Open page' }).click()
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(page.getByLabel('Page title')).toHaveValue('notes')
})

test('files that are not plain Word documents are refused with a reason', async ({ page }) => {
  await signup(page)
  await createSpace(page)
  // a macro-enabled document, whatever it is called
  await choose(
    page,
    'macro.docx',
    docx(
      p('x'),
      { 'word/vbaProject.bin': 'x' },
      '',
      'application/vnd.ms-word.document.macroEnabled.main+xml',
    ),
  )
  await expect(page.locator('p[role=alert]')).toContainText(/template|macro/i)
  // not a zip at all
  await choose(page, 'fake.docx', Buffer.from('this is plain text, not a Word file'))
  await expect(page.locator('p[role=alert]')).toContainText('not a Word')
  // wrong extension is caught before the upload
  await page
    .getByLabel('Word file')
    .setInputFiles({ name: 'old.doc', mimeType: 'application/msword', buffer: Buffer.from('x') })
  await expect(page.locator('p[role=alert]')).toContainText('saved as .docx')
  await expect(page).not.toHaveURL(/\/n\//)
})

test('the import hub has a Word section with a space picker', async ({ page }) => {
  await signup(page)
  const space = await createSpace(page)
  await page.goto(`/w/${wsOf(page)}/import`)
  const section = page.getByRole('region', { name: 'Word document' })
  await expect(section.getByLabel('Space for the Word import')).toContainText(space)
  await choose(page, 'hub.docx', docx(p('From the hub', 'Heading1') + p('Body line')))
  await expect(page).toHaveURL(/\/n\/[0-9a-f-]+$/)
  await expect(page.getByLabel('Page title')).toHaveValue('From the hub')
  await expect(page.getByRole('textbox', { name: 'Page content' })).toContainText('Body line')
})
