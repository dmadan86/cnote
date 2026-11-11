'use client'
import { FileText, Navigation, Plus, Search } from 'lucide-react'
import { useRouter } from 'next/navigation'
import { useEffect, useState } from 'react'
import { cmd } from '@/lib/api'
import { MODULES, moduleOn } from '@/lib/modules'
import { useShell } from './shell'
import { Modal } from './ui'

type Hit = { id: string; title: string; snippet: string }
type Item = { key: string; label: string; hint?: string; icon: React.ReactNode; run: () => void }

export function Palette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { ws, spaces, newPage, modules } = useShell()
  const router = useRouter()
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<Hit[]>([])
  const [active, setActive] = useState(0)

  useEffect(() => {
    if (!open) return setQ('')
    setActive(0)
  }, [open])

  useEffect(() => {
    if (q.trim().length < 2) return setHits([])
    let live = true
    const t = setTimeout(
      () =>
        cmd<Hit[]>(ws, 'search.notes', { q, limit: 8 }).then(
          (h) => live && setHits(h),
          () => live && setHits([]),
        ),
      150,
    )
    return () => {
      live = false
      clearTimeout(t)
    }
  }, [q, ws])

  const go = (href: string) => () => {
    onClose()
    router.push(href)
  }
  const needle = q.trim().toLowerCase()
  const items: Item[] = [
    ...hits.map((h) => ({
      key: h.id,
      label: h.title || 'Untitled',
      hint: h.snippet.replace(/<[^>]+>/g, ''),
      icon: <FileText size={14} />,
      run: go(`/w/${ws}/n/${h.id}`),
    })),
    ...(needle
      ? [
          {
            key: 'search',
            label: `Search for "${q.trim()}"`,
            icon: <Search size={14} />,
            run: go(`/w/${ws}/search?q=${encodeURIComponent(q.trim())}`),
          },
        ]
      : []),
    {
      key: 'create',
      label: 'Create page',
      icon: <Plus size={14} />,
      run: () => {
        onClose()
        newPage()
      },
    },
    ...MODULES.filter((m) => moduleOn(modules, m.key)).map((m) => ({
      key: `nav-${m.key}`,
      label: `Go to ${m.label}`,
      icon: <Navigation size={14} />,
      run: go(
        m.key === 'docs' && spaces[0]
          ? `/w/${ws}/s/${spaces[0].id}`
          : `/w/${ws}${m.key && m.key !== 'docs' ? `/${m.key}` : ''}`,
      ),
    })),
    {
      key: 'nav-marketplace',
      label: 'Go to Marketplace',
      icon: <Navigation size={14} />,
      run: go(`/w/${ws}/marketplace`),
    },
    {
      key: 'nav-settings',
      label: 'Go to Settings',
      icon: <Navigation size={14} />,
      run: go(`/w/${ws}/settings`),
    },
  ].filter(
    (i) =>
      !needle ||
      hits.some((h) => h.id === i.key) ||
      i.key === 'search' ||
      i.label.toLowerCase().includes(needle),
  )

  function onKey(e: React.KeyboardEvent) {
    if (!['ArrowDown', 'ArrowUp', 'Enter'].includes(e.key)) return
    e.preventDefault()
    if (e.key === 'Enter') items[active]?.run()
    else setActive((a) => Math.max(0, Math.min(a + (e.key === 'ArrowDown' ? 1 : -1), items.length - 1)))
  }

  return (
    <Modal open={open} onClose={onClose} title="Command palette" className="max-w-xl">
      <input
        // biome-ignore lint/a11y/noAutofocus: palette must take focus on open
        autoFocus
        role="combobox"
        aria-expanded="true"
        aria-controls="palette-list"
        aria-label="Search pages or run a command"
        placeholder="Search pages or run a command..."
        value={q}
        onChange={(e) => {
          setQ(e.target.value)
          setActive(0)
        }}
        onKeyDown={onKey}
        className="w-full rounded-md border border-line bg-canvas px-3 py-2"
      />
      <div id="palette-list" role="listbox" className="mt-2 max-h-80 overflow-y-auto">
        {items.map((i, idx) => (
          // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard handled by the combobox input
          <div
            key={i.key}
            role="option"
            aria-selected={idx === active}
            tabIndex={-1}
            onMouseEnter={() => setActive(idx)}
            onClick={i.run}
            className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-[13px] aria-selected:bg-hover"
          >
            <span className="text-muted">{i.icon}</span>
            <span className="truncate">{i.label}</span>
            {i.hint && <span className="ml-auto max-w-[50%] truncate text-[12px] text-muted">{i.hint}</span>}
          </div>
        ))}
      </div>
    </Modal>
  )
}
