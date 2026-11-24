import { blogMetadata, PublishedBlog } from '@/components/sites/published'

export const dynamic = 'force-dynamic'

type Params = { params: Promise<{ host: string }> }
export const generateMetadata = async ({ params }: Params) => blogMetadata((await params).host, 1)
export default async function Page({ params }: Params) {
  return <PublishedBlog host={(await params).host} n={1} />
}
