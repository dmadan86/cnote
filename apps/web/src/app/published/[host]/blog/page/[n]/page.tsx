import { notFound } from 'next/navigation'
import { blogMetadata, hasPublishedBlogPage, PublishedBlog } from '@/components/sites/published'

export const dynamic = 'force-dynamic'

// /blog/page/<n>, n from 2: a path rather than ?page=, because the proxy rewrite drops the query (design 3).
type Params = { params: Promise<{ host: string; n: string }> }
const num = (n: string) => (/^[1-9]\d{0,3}$/.test(n) ? Number(n) : 0)
export const generateMetadata = async ({ params }: Params) => {
  const p = await params
  return num(p.n) > 1 && !(await hasPublishedBlogPage(p.host)) ? blogMetadata(p.host, num(p.n)) : {}
}
export default async function Page({ params }: Params) {
  const p = await params
  if (num(p.n) < 2 || (await hasPublishedBlogPage(p.host))) notFound()
  return <PublishedBlog host={p.host} n={num(p.n)} />
}
