'use client'
import { Building2, Globe } from 'lucide-react'
import Link from 'next/link'
import { useEffect, useRef, useState } from 'react'
import { btn, btnPrimary, EmptyState, ErrorText, Modal } from '@/components/ui'
import { useCmd } from '@/lib/hooks'
import { siteApi, type Template } from '@/lib/sites'
import { DeviceToggle, PreviewFrame } from './renderer'

/** A 1280px page scaled into the card. */
function Thumb({ t }: { t: Template }) {
  const box = useRef<HTMLDivElement>(null)
  const [scale, setScale] = useState(0.25)
  useEffect(() => {
    const el = box.current
    if (!el) return
    const ro = new ResizeObserver(() => setScale(el.clientWidth / 1280))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return (
    <div
      ref={box}
      className="relative aspect-[16/10] overflow-hidden rounded-t-lg border-b border-line bg-hover"
    >
      <div
        className="pointer-events-none absolute left-0 top-0 origin-top-left"
        style={{ transform: `scale(${scale})` }}
      >
        <PreviewFrame data={t.data} decorative className="h-[800px] w-[1280px]" />
      </div>
    </div>
  )
}

function Summary({ t }: { t: Template }) {
  const c = (k: string) => String(t.data.content.find((b) => b.type === 'ContactForm')?.props?.[k] ?? '')
  return <>{[t.data.root.props.title, c('phone'), c('email'), c('address')].filter(Boolean).join(' · ')}</>
}

export function Start({
  ws,
  canCreate,
  onCreated,
}: {
  ws: string
  canCreate: boolean
  onCreated: () => void
}) {
  const tpl = useCmd<Template[]>(ws, 'site.templates')
  const [preview, setPreview] = useState<Template | null>(null)
  const [mobile, setMobile] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<Error>()
  const first = tpl.data?.[0]

  const use = async (t: Template) => {
    setBusy(true)
    setErr(undefined)
    try {
      await siteApi.create(ws, t.key)
      onCreated()
    } catch (e) {
      setErr(e as Error)
      setBusy(false)
    }
  }

  if (!canCreate)
    return (
      <div className="p-6">
        <EmptyState icon={Globe} title="Ask an admin to create your website." />
      </div>
    )
  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <div>
        <h1 className="text-xl font-semibold">Create your website</h1>
        <p className="text-[13px] text-muted">Pick a design. We'll fill in your business details.</p>
      </div>
      {first && (
        <div className="flex items-start gap-3 rounded-lg border border-line p-3">
          <Building2 size={18} className="mt-0.5 shrink-0 text-muted" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-[14px] font-medium">
              <Summary t={first} />
            </p>
            <p className="text-[12px] text-muted">Copied into your site when it's created.</p>
          </div>
          <Link href={`/w/${ws}/settings?tab=Company`} className={btn}>
            Edit
          </Link>
        </div>
      )}
      {first?.too_long?.map((f) => (
        <p key={f} className="text-[13px] text-warn">
          Your {f} is too long for the site; you can type a shorter one in the chat.
        </p>
      ))}
      <ErrorText error={err ?? tpl.error} />
      {tpl.data?.length === 0 && (
        <EmptyState icon={Globe} title="No designs yet">
          Designs will appear here soon.
        </EmptyState>
      )}
      <ul className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {tpl.data?.map((t) => (
          <li key={t.key} className="rounded-lg border border-line">
            <Thumb t={t} />
            <div className="space-y-3 p-3">
              <div>
                <h2 className="text-[14px] font-semibold">{t.name}</h2>
                <p className="text-[13px] text-muted">{t.description}</p>
              </div>
              <div className="flex gap-2">
                <button type="button" className={btn} onClick={() => setPreview(t)}>
                  Preview
                </button>
                <button type="button" className={btnPrimary} disabled={busy} onClick={() => use(t)}>
                  Use this
                </button>
              </div>
            </div>
          </li>
        ))}
      </ul>
      <Modal
        open={!!preview}
        onClose={() => setPreview(null)}
        title={`Preview: ${preview?.name ?? ''}`}
        className="max-w-6xl"
      >
        {preview && (
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <DeviceToggle mobile={mobile} onChange={setMobile} />
              <button type="button" className={btnPrimary} disabled={busy} onClick={() => use(preview)}>
                Use this design
              </button>
            </div>
            <div className="overflow-hidden rounded-lg border border-line bg-hover">
              <PreviewFrame data={preview.data} mobile={mobile} className="h-[70vh]" />
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
