import type { Metadata } from 'next'
import { PublicTableForm } from '@/components/tables/public-form'

/** Public table form (design 18.3). No session and no app chrome; the token in the link is the only authorization. */
export const metadata: Metadata = { referrer: 'no-referrer', robots: { index: false, follow: false } }

export default async function Page({ params }: { params: Promise<{ token: string }> }) {
  const { token } = await params
  return <PublicTableForm token={token} />
}
