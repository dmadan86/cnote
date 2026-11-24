import { langNative } from '@/lib/sites'
import { t } from './strings'

/**
 * The language switcher at the top of a published page: every name in its own script, each a plain link to the same
 * page in that language (no JavaScript). The current language is marked, not linked.
 */
export function LangSwitcher({
  current,
  items,
}: {
  current: string
  items: { code: string; href: string }[]
}) {
  return (
    <nav
      aria-label={t(current, 'lang_switcher')}
      className="flex justify-end border-b border-[var(--site-border)] px-5 py-1.5 text-[13px] md:px-8"
    >
      <ul className="flex flex-wrap gap-x-4 gap-y-1">
        {items.map((l) => (
          <li key={l.code}>
            {l.code === current ? (
              <span lang={l.code} aria-current="true" className="font-semibold">
                {langNative(l.code)}
              </span>
            ) : (
              <a href={l.href} hrefLang={l.code} lang={l.code} className="underline-offset-4 hover:underline">
                {langNative(l.code)}
              </a>
            )}
          </li>
        ))}
      </ul>
    </nav>
  )
}
