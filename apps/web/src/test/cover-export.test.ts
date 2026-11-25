// docs-parity C1 5.3: the page cover is part of the single-page export, above the title, embedded like any page image.
import { Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { describe, expect, it } from 'vitest'
import { buildExportDom, buildHtmlFile } from '../components/docs/export'

const WS = '7b3f1c2e-5a4d-4e6f-8a9b-0c1d2e3f4a5b'
const SRC = `/api/w/${WS}/files/0f0e0d0c-0b0a-4908-8706-050403020100`
const PNG = new Blob([new Uint8Array([137, 80, 78, 71])], { type: 'image/png' })
const meta = { title: 'Trip', status: null, labels: [], updatedAt: '25 Nov 2026' }

describe('cover in the export', () => {
  const e = new Editor({
    element: document.createElement('div'),
    extensions: [StarterKit],
    content: '<p>Body</p>',
  })

  it('shows the cover above the title, embedded, with the saved position', async () => {
    const { root } = await buildExportDom(
      e,
      { ...meta, cover: { src: SRC, y: 20 } },
      { fetchImage: async () => PNG },
    )
    const img = root.querySelector('header img.cn-cover')
    expect(img?.getAttribute('src')).toMatch(/^data:image\/png;base64,/)
    expect(img?.getAttribute('style')).toContain('50% 20%')
    expect(img?.getAttribute('alt')).toBe('Cover of Trip')
    expect(root.querySelector('header')?.firstElementChild).toBe(img)
    expect(buildHtmlFile(root, meta)).toContain('cn-cover')
  })

  it('has none without a cover, and refuses a source that is not a page file', async () => {
    expect((await buildExportDom(e, meta)).root.querySelector('img')).toBeNull()
    const bad = await buildExportDom(e, { ...meta, cover: { src: 'https://evil.test/x.png', y: 50 } })
    expect(bad.root.querySelector('img')).toBeNull()
  })
})
