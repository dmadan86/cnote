import type { Metadata } from 'next'
import { AcceptInvite } from '@/components/invites/accept'

/** Invitation link. The token in the path is the credential: no referrer, no indexing, nothing cached. */
export const metadata: Metadata = {
  title: 'Join workspace',
  referrer: 'no-referrer',
  robots: { index: false, follow: false },
}

export default async function Page({ params }: { params: Promise<{ token: string }> }) {
  const { token } = await params
  return <AcceptInvite token={token} />
}
