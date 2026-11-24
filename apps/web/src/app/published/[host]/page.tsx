import { PublishedPage, publishedMetadata } from '@/components/sites/published'

// Rendered per request from one API read (design 17.3); the proxy adds Cache-Control: public, max-age=60.
export const dynamic = 'force-dynamic'

type Params = { params: Promise<{ host: string }> }
export const generateMetadata = async ({ params }: Params) => publishedMetadata((await params).host, '')
export default async function Page({ params }: Params) {
  return <PublishedPage host={(await params).host} slug="" />
}
