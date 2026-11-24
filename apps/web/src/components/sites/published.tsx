import { type PublicSite, reportHref, siteSummary, themeStyle, waHref } from '@/lib/sites'
import { BLOCKS, SiteBadge } from './blocks'
import { ContactFormLive } from './contact-form'

/**
 * The published page: the same BLOCKS and theme variables as the preview, no selection chrome, no cookies.
 * A server component; the only client code is the live contact form. Block ids stay as anchors (#ContactForm-…).
 */
export function Published({ site }: { site: PublicSite }) {
  const { data } = site
  const sum = siteSummary(data)
  const assetBase = `/api/sites/img/${site.site_id}`
  return (
    <div style={themeStyle(data.root?.props?.theme)} className="min-h-screen">
      {(data.content ?? [])
        .filter((b) => b.type in BLOCKS)
        .map((b) => {
          const id = String(b.props?.id ?? '')
          const C = BLOCKS[b.type as keyof typeof BLOCKS]
          const form =
            b.type === 'ContactForm' ? (
              <ContactFormLive
                siteId={site.site_id}
                business={sum.name}
                phone={String(b.props.phone ?? '') || undefined}
                whatsapp={waHref(b.props.phone)}
                submitLabel={String(b.props.submit_label ?? '') || undefined}
                paused={site.form_paused}
              />
            ) : undefined
          return (
            <div key={id || b.type} id={id}>
              <C {...b.props} assetBase={assetBase} form={form} />
            </div>
          )
        })}
      <SiteBadge pro={site.pro} home={process.env.PUBLIC_BASE_URL ?? '/'} report={reportHref(site.url)} />
    </div>
  )
}
