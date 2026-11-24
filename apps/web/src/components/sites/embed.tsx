'use client'
import { Play } from 'lucide-react'
import { useState } from 'react'

const LABEL: Record<string, [string, string]> = {
  youtube: ['YouTube', 'Load video'],
  google_maps: ['Google Maps', 'Load map'],
  google_forms: ['Google Forms', 'Load form'],
  calendly: ['Calendly', 'Load calendar'],
}

/**
 * Design 17.7: a placeholder with the provider name and no thumbnail or preconnect. The iframe is inserted only
 * on click; without JS the placeholder is a plain link to the provider page.
 */
export function EmbedFrame({
  provider,
  href,
  src,
  title,
}: {
  provider: string
  href: string
  src: string
  title: string
}) {
  const [on, setOn] = useState(false)
  const [name, action] = LABEL[provider] ?? ['Embed', 'Load']
  const tall = provider === 'google_forms' || provider === 'calendly'
  const box = `w-full rounded-[var(--site-radius)] border border-[var(--site-border)] ${tall ? 'h-[640px]' : 'aspect-video'}`
  if (on)
    return (
      <iframe
        src={src}
        title={title || name}
        className={box}
        loading="lazy"
        allowFullScreen
        referrerPolicy="no-referrer"
        sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
      />
    )
  return (
    <a
      href={href}
      rel="noopener noreferrer"
      onClick={(e) => {
        e.preventDefault()
        setOn(true)
      }}
      className={`${box} flex flex-col items-center justify-center gap-2 bg-[var(--site-tint)] text-center focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--site-primary)] ${tall ? 'min-h-48 !h-auto py-12' : ''}`}
    >
      <Play size={28} aria-hidden="true" />
      <span className="font-semibold">{action}</span>
      <span className="text-[13px] text-[var(--site-muted)]">
        {name} · loads from {name} when you click
      </span>
    </a>
  )
}
