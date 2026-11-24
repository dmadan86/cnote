import {
  blogMetadata,
  hasPublishedBlogPage,
  PublishedBlog,
  PublishedPage,
  publishedMetadata,
} from '@/components/sites/published'

export const dynamic = 'force-dynamic'

type Params = { params: Promise<{ host: string }> }
export const generateMetadata = async ({ params }: Params) => {
  const { host } = await params
  return (await hasPublishedBlogPage(host)) ? publishedMetadata(host, 'blog') : blogMetadata(host, 1)
}
export default async function Page({ params }: Params) {
  const { host } = await params
  return (await hasPublishedBlogPage(host)) ? (
    <PublishedPage host={host} slug="blog" />
  ) : (
    <PublishedBlog host={host} n={1} />
  )
}
