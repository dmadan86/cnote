import { HomePage } from '@/components/home/page'

// Read per request: HOME_WIDGETS is a runtime flag, never frozen at build.
export const dynamic = 'force-dynamic'

export default function Page() {
  return <HomePage widgets={process.env.HOME_WIDGETS === '1'} />
}
