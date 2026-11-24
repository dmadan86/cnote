import { PublishedPage, publishedMetadata } from '@/components/sites/published'

// A non-default language's Home: the proxy rewrites /<lang>/... under `l-`, which can never be a page slug (design 18.6).
export const dynamic = 'force-dynamic'

type Params = { params: Promise<{ host: string; lang: string }> }
export const generateMetadata = async ({ params }: Params) => {
  const p = await params
  return publishedMetadata(p.host, '', p.lang)
}
export default async function Page({ params }: Params) {
  const p = await params
  return <PublishedPage host={p.host} slug="" lang={p.lang} />
}
