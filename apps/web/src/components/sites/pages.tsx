'use client'
import { Check, ChevronDown, MoreHorizontal, Plus } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, Modal } from '@/components/ui'
import { PAGE_TEMPLATES, pageList, type Site, type SiteData, siteApi, slugify, validSlug } from '@/lib/sites'

/** The builder's apply: runs the edit, shows its error, and says whether it worked. */
type Apply = (fn: () => Promise<{ site: Site; data: SiteData }>) => Promise<boolean>
const LEGAL = 'Template — not legal advice. Read it and have it checked before you publish.'

/** `[Home ▾]` in the top bar: the page list, rename, delete and Add page (design UX section 3). */
export function PageSwitcher({
  ws,
  site,
  data,
  page,
  onPage,
  apply,
  disabled,
}: {
  ws: string
  site: Site
  data: SiteData
  page: string
  onPage: (slug: string) => void
  apply: Apply
  disabled: boolean
}) {
  const pages = pageList(data)
  const cur = pages.find((p) => p.slug === page) ?? pages[0]
  const [open, setOpen] = useState(false)
  const [add, setAdd] = useState(false)
  const [del, setDel] = useState<string>()
  const [rename, setRename] = useState<string>()
  const [name, setName] = useState('')
  const [menu, setMenu] = useState<string>()
  const box = useRef<HTMLDivElement>(null)
  const max = site.pro ? 10 : 4 // pages besides Home (design 16.7); the server enforces it too
  const full = pages.length - 1 >= max
  useEffect(() => {
    if (!open) return
    const away = (e: MouseEvent) => !box.current?.contains(e.target as Node) && setOpen(false)
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', away)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', away)
      document.removeEventListener('keydown', esc)
    }
  }, [open])
  return (
    <div ref={box} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Page: ${cur.title}`}
        className={`${btn} max-w-[160px]`}
        onClick={() => setOpen((o) => !o)}
      >
        <span className="truncate">{cur.title}</span> <ChevronDown size={14} aria-hidden="true" />
      </button>
      {open && (
        <div
          role="menu"
          aria-label="Pages"
          className="absolute left-0 top-full z-30 mt-1 w-72 rounded-lg border border-line bg-canvas p-1 text-[13px] shadow-lg"
        >
          {pages.map((p) => (
            <div key={p.slug || 'home'} className="relative flex items-center gap-1 rounded hover:bg-hover">
              {rename === p.slug && p.slug ? (
                <form
                  className="flex flex-1 gap-1 p-1"
                  onSubmit={(e) => {
                    e.preventDefault()
                    if (name.trim() && name.trim() !== p.title)
                      apply(() =>
                        siteApi.setPage(ws, site.id, site.draft_version_id, {
                          page: p.slug,
                          title: name.trim(),
                        }),
                      )
                    setRename(undefined)
                  }}
                >
                  <input
                    aria-label="Page name"
                    value={name}
                    maxLength={80}
                    // biome-ignore lint/a11y/noAutofocus: the person just chose Rename
                    autoFocus
                    onChange={(e) => setName(e.target.value)}
                    className="min-w-0 flex-1 rounded-md border border-line bg-canvas px-2 py-1 text-[13px]"
                  />
                  <button type="submit" className={btn}>
                    Save
                  </button>
                </form>
              ) : (
                <>
                  <button
                    type="button"
                    role="menuitem"
                    className="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left"
                    onClick={() => {
                      onPage(p.slug)
                      setOpen(false)
                    }}
                  >
                    <Check size={14} aria-hidden="true" className={p.slug === cur.slug ? '' : 'invisible'} />
                    <span className="truncate">{p.title}</span>
                    {(p.slug === 'privacy' || p.slug === 'terms') && (
                      <span className="shrink-0 text-[11px] text-muted">footer</span>
                    )}
                  </button>
                  {p.slug && (
                    <button
                      type="button"
                      aria-label={`Options for ${p.title}`}
                      aria-expanded={menu === p.slug}
                      className="rounded p-1.5 hover:bg-line/40"
                      onClick={() => setMenu(menu === p.slug ? undefined : p.slug)}
                    >
                      <MoreHorizontal size={14} />
                    </button>
                  )}
                </>
              )}
              {menu === p.slug && p.slug && (
                <div className="absolute right-1 top-full z-40 w-36 rounded-md border border-line bg-canvas p-1 shadow-lg">
                  <button
                    type="button"
                    className="block w-full rounded px-2 py-1.5 text-left hover:bg-hover"
                    onClick={() => {
                      setName(p.title)
                      setRename(p.slug)
                      setMenu(undefined)
                    }}
                  >
                    Rename
                  </button>
                  <button
                    type="button"
                    className="block w-full rounded px-2 py-1.5 text-left text-bad hover:bg-hover"
                    onClick={() => {
                      setDel(p.slug)
                      setMenu(undefined)
                    }}
                  >
                    Delete
                  </button>
                </div>
              )}
            </div>
          ))}
          <hr className="my-1 border-line" />
          <button
            type="button"
            role="menuitem"
            disabled={disabled || full}
            title={full ? `Your plan has up to ${max} pages besides Home` : undefined}
            className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-hover disabled:opacity-50"
            onClick={() => {
              setAdd(true)
              setOpen(false)
            }}
          >
            <Plus size={14} aria-hidden="true" /> Add page
          </button>
          {full && (
            <p className="px-2 pb-1 text-[12px] text-muted">Your plan has up to {max} pages besides Home.</p>
          )}
        </div>
      )}
      <AddPage
        open={add}
        domain={site.url?.replace(/^https?:\/\//, '') ?? ''}
        taken={pages.map((p) => p.slug)}
        onClose={() => setAdd(false)}
        onAdd={async (title, slug, template) => {
          if (
            await apply(() =>
              siteApi.addPage(ws, site.id, site.draft_version_id, title, slug, template || undefined),
            )
          ) {
            onPage(slug)
            setAdd(false)
          }
        }}
      />
      <Modal
        open={!!del}
        onClose={() => setDel(undefined)}
        title={`Delete the ${pages.find((p) => p.slug === del)?.title} page?`}
      >
        <div className="space-y-4 text-[13px]">
          <p>
            It is removed from your menu and website. Links to it go to Home. You can bring it back from
            Version history.
          </p>
          <div className="flex justify-end gap-2">
            <button type="button" className={btn} onClick={() => setDel(undefined)}>
              Cancel
            </button>
            <button
              type="button"
              className="inline-flex items-center rounded-md bg-bad px-3 py-1.5 text-[13px] font-medium text-canvas"
              onClick={async () => {
                const slug = del as string
                setDel(undefined)
                if (
                  (await apply(() => siteApi.removePage(ws, site.id, site.draft_version_id, slug))) &&
                  page === slug
                )
                  onPage('')
              }}
            >
              Delete page
            </button>
          </div>
        </div>
      </Modal>
    </div>
  )
}

function AddPage({
  open,
  domain,
  taken,
  onClose,
  onAdd,
}: {
  open: boolean
  domain: string
  taken: string[]
  onClose: () => void
  onAdd: (title: string, slug: string, template: string) => Promise<void>
}) {
  const [tpl, setTpl] = useState<string>('about')
  const [title, setTitle] = useState('About us')
  const [slug, setSlug] = useState('about-us')
  const [typedSlug, setTypedSlug] = useState(false)
  const [busy, setBusy] = useState(false)
  const t = PAGE_TEMPLATES.find((x) => x.key === tpl) ?? PAGE_TEMPLATES[0]
  // biome-ignore lint/correctness/useExhaustiveDependencies: reset each time the dialog opens, not when the list changes
  useEffect(() => {
    if (!open) return
    setTpl('about')
    setTitle('About us')
    setTypedSlug(false)
    setSlug(free('about-us'))
  }, [open])
  const free = (base: string) => {
    let s = base
    for (let n = 2; taken.includes(s) && n < 20; n++) s = `${base}-${n}`.slice(0, 40)
    return s
  }
  const setName = (v: string) => {
    setTitle(v)
    if (!typedSlug) setSlug(free(slugify(v)))
  }
  const bad = !title.trim() || !validSlug(slug) || taken.includes(slug)
  return (
    <Modal open={open} onClose={onClose} title="Add a page" className="max-w-lg">
      <form
        className="space-y-4 text-[13px]"
        onSubmit={async (e) => {
          e.preventDefault()
          if (bad) return
          setBusy(true)
          try {
            await onAdd(title.trim(), slug, t.legal ? t.key : '')
          } finally {
            setBusy(false)
          }
        }}
      >
        <fieldset className="space-y-1">
          <legend className="mb-1 text-[12px] font-medium text-muted">Start from</legend>
          {PAGE_TEMPLATES.map((x) => (
            <label
              key={x.key || 'blank'}
              className="flex items-center gap-2 rounded px-1 py-1 hover:bg-hover"
            >
              <input
                type="radio"
                name="page-template"
                checked={tpl === x.key}
                onChange={() => {
                  setTpl(x.key)
                  setTypedSlug(false)
                  setTitle(x.title)
                  setSlug(free(x.legal ? x.key : slugify(x.title))) // legal pages keep their conventional address
                }}
              />
              {x.name}
              {x.legal && <span className="text-[11px] text-muted">Template — not legal advice</span>}
            </label>
          ))}
        </fieldset>
        {t.legal && <p className="rounded-md bg-warn-bg px-2.5 py-1.5 text-[12px] text-warn">{LEGAL}</p>}
        {!t.legal && (
          <p className="text-muted">
            The page starts empty. Add sections from the Edit tab or ask the assistant.
          </p>
        )}
        <div className="space-y-1">
          <label htmlFor="page-name" className="text-[12px] font-medium text-muted">
            Page name
          </label>
          <input
            id="page-name"
            value={title}
            maxLength={80}
            onChange={(e) => setName(e.target.value)}
            className="w-full rounded-md border border-line bg-canvas px-2.5 py-1.5 text-[14px]"
          />
        </div>
        <div className="space-y-1">
          <label htmlFor="page-slug" className="text-[12px] font-medium text-muted">
            Address
          </label>
          <div className="flex items-center gap-1">
            <span className="truncate text-muted">{domain}/</span>
            <input
              id="page-slug"
              value={slug}
              maxLength={40}
              onChange={(e) => {
                setTypedSlug(true)
                setSlug(e.target.value.toLowerCase())
              }}
              className="min-w-0 flex-1 rounded-md border border-line bg-canvas px-2.5 py-1.5 text-[14px]"
            />
          </div>
          {slug && bad && title.trim() && (
            <p className="text-[12px] text-bad">
              {taken.includes(slug)
                ? 'You already have a page at this address.'
                : 'Use lowercase letters, numbers and hyphens.'}
            </p>
          )}
        </div>
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary} disabled={bad || busy}>
            Add page
          </button>
        </div>
      </form>
    </Modal>
  )
}
