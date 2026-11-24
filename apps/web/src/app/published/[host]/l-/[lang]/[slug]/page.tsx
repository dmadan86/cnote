import { PublishedPage, publishedMetadata } from '@/components/sites/published'

export const dynamic = 'force-dynamic'

type Params = { params: Promise<{ host: string; lang: string; slug: string }> }
export const generateMetadata = async ({ params }: Params) => {
  const p = await params
  return publishedMetadata(p.host, p.slug, p.lang)
}
export default async function Page({ params }: Params) {
  const p = await params
  return <PublishedPage host={p.host} slug={p.slug} lang={p.lang} />
}
