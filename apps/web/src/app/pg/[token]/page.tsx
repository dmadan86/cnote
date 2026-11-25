import type { Metadata } from 'next'
import { PublicPageView } from '@/components/docs/public-page'

/** Public page (docs-parity-b 4.2). No session and no app chrome; the token in the link is the only authorization. */
export const metadata: Metadata = {
  title: 'Shared page',
  referrer: 'no-referrer',
  robots: { index: false, follow: false },
}

export default async function Page({ params }: { params: Promise<{ token: string }> }) {
  const { token } = await params
  return <PublicPageView token={token} />
}
