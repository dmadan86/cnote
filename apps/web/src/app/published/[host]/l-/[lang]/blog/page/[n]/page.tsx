import { notFound } from 'next/navigation'
import { blogMetadata, hasPublishedBlogPage, PublishedBlog } from '@/components/sites/published'

export const dynamic = 'force-dynamic'

type Params = { params: Promise<{ host: string; lang: string; n: string }> }
const num = (n: string) => (/^[1-9]\d{0,3}$/.test(n) ? Number(n) : 0)
export const generateMetadata = async ({ params }: Params) => {
  const p = await params
  return num(p.n) > 1 && !(await hasPublishedBlogPage(p.host)) ? blogMetadata(p.host, num(p.n), p.lang) : {}
}
export default async function Page({ params }: Params) {
  const p = await params
  if (num(p.n) < 2 || (await hasPublishedBlogPage(p.host))) notFound()
  return <PublishedBlog host={p.host} n={num(p.n)} lang={p.lang} />
}
