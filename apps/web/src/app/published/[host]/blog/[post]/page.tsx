import { PublishedPost, postMetadata } from '@/components/sites/published'

export const dynamic = 'force-dynamic'

type Params = { params: Promise<{ host: string; post: string }> }
export const generateMetadata = async ({ params }: Params) => {
  const p = await params
  return postMetadata(p.host, p.post)
}
export default async function Page({ params }: Params) {
  const p = await params
  return <PublishedPost host={p.host} slug={p.post} />
}
