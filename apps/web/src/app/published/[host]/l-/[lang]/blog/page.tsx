import {
  blogMetadata,
  hasPublishedBlogPage,
  PublishedBlog,
  PublishedPage,
  publishedMetadata,
} from '@/components/sites/published'

export const dynamic = 'force-dynamic'

type Params = { params: Promise<{ host: string; lang: string }> }
export const generateMetadata = async ({ params }: Params) => {
  const { host, lang } = await params
  return (await hasPublishedBlogPage(host))
    ? publishedMetadata(host, 'blog', lang)
    : blogMetadata(host, 1, lang)
}
export default async function Page({ params }: Params) {
  const { host, lang } = await params
  return (await hasPublishedBlogPage(host)) ? (
    <PublishedPage host={host} slug="blog" lang={lang} />
  ) : (
    <PublishedBlog host={host} n={1} lang={lang} />
  )
}
