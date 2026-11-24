import { SiteHome } from './home'

// Read per request: the sites domain and support channels are runtime settings, never frozen at build.
export const dynamic = 'force-dynamic'

export default function Page() {
  return (
    <SiteHome
      domain={process.env.SITES_DOMAIN || undefined}
      support={{
        whatsapp: process.env.SUPPORT_WHATSAPP || undefined,
        email: process.env.SUPPORT_EMAIL || undefined,
      }}
    />
  )
}
