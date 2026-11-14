import Link from 'next/link'

/** Shown when a home tile opened a list pre-filtered through the URL; links back to the full list. */
export function FilterBanner({ label, href }: { label: string; href: string }) {
  return (
    <p role="status" className="mb-3 flex items-center gap-2 text-[13px] text-muted">
      Showing: {label}
      <Link href={href} className="underline">
        Show all
      </Link>
    </p>
  )
}

export const today = () => new Date().toISOString().slice(0, 10)
