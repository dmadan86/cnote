import { expect, test } from '@playwright/test'
import { signup, uniq } from './helpers'

// Published blog: post page, RSS, sitemap, pagination path. Needs the same env as site-publish.spec.ts.
const port = new URL(process.env.BASE_URL ?? 'http://localhost:3000').port || '3000'

test('Blog: a published post is served with SEO, in RSS and in the sitemap; drafts are not', async ({
  page,
  browser,
}) => {
  await signup(page)
  const ws = new URL(page.url()).pathname.match(/^\/w\/([0-9a-f-]+)/)?.[1] as string
  const cmd = async (name: string, data: unknown) => {
    const r = await page.request.post(`/api/w/${ws}/commands/${name}`, { data })
    expect(r.ok(), `${name}: ${await r.text()}`).toBeTruthy()
    return r.json()
  }
  await page.goto(`/w/${ws}/site`)
  await page.getByRole('button', { name: 'Use this' }).first().click()
  await expect(page.getByText('Not published yet')).toBeVisible()
  const [site] = await cmd('site.list', {})
  const got = await cmd('site.get', { id: site.id })
  const sub = `e2e-${uniq()}`
  await cmd('site.publish', { id: site.id, expected_version_id: got.site.draft_version_id, subdomain: sub })

  const body = (text: string) => ({
    type: 'doc',
    content: [
      { type: 'heading', attrs: { level: 2 }, content: [{ type: 'text', text: 'Why ghee' }] },
      {
        type: 'paragraph',
        content: [
          { type: 'text', text, marks: [{ type: 'bold' }] },
          { type: 'text', text: ' <script>x</script> & more' },
        ],
      },
    ],
  })
  const save = (title: string, slug: string) =>
    cmd('site.post_save', {
      id: site.id,
      title,
      slug,
      description: `About ${title}`,
      body: body('Fresh daily'),
    })
  const live = await save('How we make kaju katli', 'kaju-katli')
  await save('Unfinished draft', 'unfinished')
  await cmd('site.post_publish', { id: site.id, post_id: live.id, expected_rev: live.rev, publish: true })

  const siteUrl = `http://${sub}.localhost:${port}`
  const ctx = await browser.newContext()
  const v = await ctx.newPage()
  const res = await v.goto(`${siteUrl}/blog/kaju-katli`)
  expect(res?.status()).toBe(200)
  await expect(v.getByRole('heading', { level: 1 })).toHaveText('How we make kaju katli')
  await expect(v.getByRole('heading', { level: 2, name: 'Why ghee' })).toBeVisible()
  // The body text is text, never markup.
  await expect(v.getByText('<script>x</script> & more')).toBeVisible()
  await expect(v.locator('article script')).toHaveCount(0)
  await expect(v.locator('link[rel=canonical]')).toHaveAttribute('href', `${siteUrl}/blog/kaju-katli`)
  await expect(v.locator('meta[property="og:type"]')).toHaveAttribute('content', 'article')
  await expect(v.locator('link[rel=alternate][type="application/rss+xml"]')).toHaveCount(1)
  const ld = JSON.parse((await v.locator('script[type="application/ld+json"]').first().textContent()) ?? '{}')
  expect(ld['@type']).toBe('BlogPosting')
  expect(ld.headline).toBe('How we make kaju katli')

  const list = await v.goto(`${siteUrl}/blog`)
  expect(list?.status()).toBe(200)
  await expect(v.getByRole('link', { name: 'How we make kaju katli' })).toBeVisible()
  await expect(v.getByText('Unfinished draft')).toHaveCount(0)
  expect((await v.goto(`${siteUrl}/blog/unfinished`))?.status()).toBe(404)
  expect((await v.goto(`${siteUrl}/blog/page/2`))?.status()).toBe(404)

  const rss = await ctx.request.get(`${siteUrl}/blog/rss.xml`)
  expect(rss.status()).toBe(200)
  expect(rss.headers()['content-type']).toContain('application/rss+xml')
  const xml = await rss.text()
  expect(xml).toContain(`<link>${siteUrl}/blog/kaju-katli</link>`)
  expect(xml).not.toContain('Unfinished')
  const map = await (await ctx.request.get(`${siteUrl}/sitemap.xml`)).text()
  expect(map).toContain(`${siteUrl}/blog</loc>`)
  expect(map).toContain(`${siteUrl}/blog/kaju-katli</loc>`)
  expect(map).not.toContain('unfinished')
  await ctx.close()
})
