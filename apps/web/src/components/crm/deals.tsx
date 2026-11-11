'use client'
import {
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
} from '@dnd-kit/core'
import { Plus } from 'lucide-react'
import Link from 'next/link'
import { useRouter } from 'next/navigation'
import { useState } from 'react'
import { newDraft } from '@/components/billing/bits'
import { type Contact, type Deal, money, parseMinor, toRupees } from '@/components/billing/fmt'
import { useShell } from '@/components/shell'
import { UseTemplateButton } from '@/components/templates/use-template'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { NeedSpace } from './contacts'

const STAGES: [key: string, label: string, dot: string][] = [
  ['lead', 'Lead', 'bg-slate-400'],
  ['qualified', 'Qualified', 'bg-sky-500'],
  ['proposal', 'Proposal', 'bg-violet-500'],
  ['negotiation', 'Negotiation', 'bg-amber-500'],
  ['won', 'Won', 'bg-green-600'],
  ['lost', 'Lost', 'bg-red-500'],
]
const stageLabel = (k: string) => STAGES.find(([s]) => s === k)?.[1] ?? k

function DealForm({ stage, onClose, onSaved }: { stage: string; onClose: () => void; onSaved: () => void }) {
  const { ws, spaces } = useShell()
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const [err, setErr] = useState<string>()
  return (
    <Modal open title={`New deal in ${stageLabel(stage)}`} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>
          const amount = f.amount ? parseMinor(f.amount) : 0
          if (amount === null) return setErr('Amount must be a number like 50000 or 1250.50')
          try {
            await cmd(ws, 'deal.create', {
              space_id: spaces[0].id,
              title: f.title,
              stage,
              amount_minor: amount,
              contact_id: f.contact_id || undefined,
              expected_close: f.expected_close || undefined,
            })
            onSaved()
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <Field label="Deal title">
          {(id) => <input id={id} name="title" required className={inputCls} />}
        </Field>
        <Field label="Amount (INR)">
          {(id) => <input id={id} name="amount" inputMode="decimal" className={inputCls} />}
        </Field>
        <Field label="Contact">
          {(id) => (
            <select id={id} name="contact_id" className={inputCls}>
              <option value="">None</option>
              {contacts.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Expected close">
          {(id) => <input id={id} name="expected_close" type="date" className={inputCls} />}
        </Field>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Create deal
          </button>
        </div>
      </form>
    </Modal>
  )
}

function Card({ deal, contact }: { deal: Deal; contact?: string }) {
  const { ws } = useShell()
  const { attributes, listeners, setNodeRef, transform, isDragging } = useDraggable({ id: deal.id })
  return (
    <li
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      aria-label={`Deal ${deal.title}`}
      style={transform ? { transform: `translate(${transform.x}px, ${transform.y}px)` } : undefined}
      className={`relative cursor-grab touch-none rounded-md border border-line bg-canvas p-2.5 shadow-sm ${isDragging ? 'z-20 opacity-80 shadow-lg' : ''}`}
    >
      <Link
        href={`/w/${ws}/crm/deals/${deal.id}`}
        className="block text-[14px] font-medium hover:underline"
        draggable={false}
      >
        {deal.title}
      </Link>
      {contact && <p className="text-[12px] text-muted">{contact}</p>}
      <p className="mt-1 text-[13px] tabular-nums">{money(deal.amount_minor)}</p>
    </li>
  )
}

function Column({
  stage,
  deals,
  contacts,
  onAdd,
}: {
  stage: (typeof STAGES)[number]
  deals: Deal[]
  contacts: Map<string, string>
  onAdd: () => void
}) {
  const { setNodeRef, isOver } = useDroppable({ id: stage[0] })
  const sum = deals.reduce((s, d) => s + d.amount_minor, 0)
  return (
    <section
      ref={setNodeRef}
      aria-label={`${stage[1]} stage`}
      className={`flex min-w-40 max-w-72 flex-1 flex-col rounded-lg bg-side ${isOver ? 'ring-2 ring-accent' : ''}`}
    >
      <header className="flex items-center gap-2 px-3 py-2 text-[13px] font-medium">
        <span aria-hidden className={`h-2 w-2 rounded-full ${stage[2]}`} />
        {stage[1]}
        <span className="text-muted" data-testid={`count-${stage[0]}`}>
          {deals.length}
        </span>
      </header>
      <ul className="min-h-24 flex-1 space-y-2 px-2">
        {deals.map((d) => (
          <Card key={d.id} deal={d} contact={d.contact_id ? contacts.get(d.contact_id) : undefined} />
        ))}
      </ul>
      <footer className="flex items-center justify-between px-3 py-2 text-[12px] text-muted">
        <span className="tabular-nums">{money(sum)}</span>
        <button type="button" onClick={onAdd} className="flex items-center gap-1 hover:text-fg">
          <Plus size={13} /> New deal
          <span className="sr-only"> in {stage[1]}</span>
        </button>
      </footer>
    </section>
  )
}

export function Board() {
  const { ws, spaces } = useShell()
  const deals = useCmd<Deal[]>(ws, 'deal.list', { limit: 500 })
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const [moved, setMoved] = useState<Record<string, string>>({}) // optimistic stage per deal
  const [adding, setAdding] = useState<string>()
  const [err, setErr] = useState<string>()
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor),
  )
  const names = new Map(contacts.data?.map((c) => [c.id, c.name]))

  const onDragEnd = async (e: DragEndEvent) => {
    const id = String(e.active.id)
    const to = e.over && String(e.over.id)
    const deal = deals.data?.find((d) => d.id === id)
    if (!to || !deal || (moved[id] ?? deal.stage) === to) return
    setMoved((m) => ({ ...m, [id]: to }))
    try {
      await cmd(ws, 'deal.move_stage', { id, stage: to })
      setErr(undefined)
    } catch (x) {
      setErr((x as Error).message)
    }
    deals.reload()
    setMoved(({ [id]: _, ...rest }) => rest)
  }

  if (!spaces.length) return <NeedSpace />
  return (
    <div>
      <ErrorText error={err ?? deals.error} />
      <DndContext sensors={sensors} onDragEnd={onDragEnd}>
        <div className="flex gap-3 overflow-x-auto pb-4">
          {STAGES.map((s) => (
            <Column
              key={s[0]}
              stage={s}
              contacts={names}
              deals={(deals.data ?? []).filter((d) => (moved[d.id] ?? d.stage) === s[0])}
              onAdd={() => setAdding(s[0])}
            />
          ))}
        </div>
      </DndContext>
      {adding && (
        <DealForm
          stage={adding}
          onClose={() => setAdding(undefined)}
          onSaved={() => {
            setAdding(undefined)
            deals.reload()
          }}
        />
      )}
    </div>
  )
}

export function DealDetail({ id }: { id: string }) {
  const { ws, spaces } = useShell()
  const router = useRouter()
  const list = useCmd<Deal[]>(ws, 'deal.list', { limit: 500 })
  const contacts = useCmd<Contact[]>(ws, 'contact.list', { limit: 500 })
  const [err, setErr] = useState<string>()
  const [msg, setMsg] = useState<string>()
  const deal = list.data?.find((d) => d.id === id)
  if (list.error) return <ErrorText error={list.error} />
  if (!list.data) return <p className="p-6 text-muted">Loading...</p>
  if (!deal) return <p className="p-6 text-muted">Deal not found.</p>
  const contact = contacts.data?.find((c) => c.id === deal.contact_id)
  const fail = (x: unknown) => setErr((x as Error).message)

  return (
    <div className="mx-auto max-w-3xl space-y-4 px-6 py-8">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-semibold">{deal.title}</h1>
        <span className="ml-auto">
          <UseTemplateButton sourceKind="deal" sourceId={deal.id} />
        </span>
        <button
          type="button"
          className={btnPrimary}
          disabled={!contact}
          title={contact ? undefined : 'Link a contact first'}
          onClick={async () => {
            if (!contact) return
            try {
              const draft = await newDraft(ws, spaces[0].id, contact.id, {
                description: deal.title,
                unit_price_minor: deal.amount_minor,
              })
              router.push(`/w/${ws}/billing/${draft}`)
            } catch (x) {
              fail(x)
            }
          }}
        >
          Create invoice
        </button>
      </div>
      <ErrorText error={err} />
      <form
        key={`${deal.title}${deal.amount_minor}${deal.contact_id}${deal.stage}`}
        className="grid grid-cols-2 gap-3 rounded-lg border border-line p-4"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>
          const amount = parseMinor(f.amount)
          if (amount === null) return setErr('Amount must be a number like 50000 or 1250.50')
          try {
            await cmd(ws, 'deal.update', {
              id,
              title: f.title,
              amount_minor: amount,
              contact_id: f.contact_id || undefined,
              expected_close: f.expected_close || undefined,
              probability: Number(f.probability),
            })
            if (f.stage !== deal.stage) await cmd(ws, 'deal.move_stage', { id, stage: f.stage })
            setErr(undefined)
            setMsg('Saved')
            list.reload()
          } catch (x) {
            fail(x)
          }
        }}
      >
        <Field label="Title">
          {(i) => <input id={i} name="title" required defaultValue={deal.title} className={inputCls} />}
        </Field>
        <Field label="Stage">
          {(i) => (
            <select id={i} name="stage" defaultValue={deal.stage} className={inputCls}>
              {STAGES.map(([k, l]) => (
                <option key={k} value={k}>
                  {l}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Amount (INR)">
          {(i) => (
            <input
              id={i}
              name="amount"
              inputMode="decimal"
              defaultValue={toRupees(deal.amount_minor)}
              className={inputCls}
            />
          )}
        </Field>
        <Field label="Contact">
          {(i) => (
            <select id={i} name="contact_id" defaultValue={deal.contact_id ?? ''} className={inputCls}>
              <option value="">None</option>
              {contacts.data?.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Expected close">
          {(i) => (
            <input
              id={i}
              name="expected_close"
              type="date"
              defaultValue={deal.expected_close ?? ''}
              className={inputCls}
            />
          )}
        </Field>
        <Field label="Probability (%)">
          {(i) => (
            <input
              id={i}
              name="probability"
              type="number"
              min={0}
              max={100}
              defaultValue={deal.probability}
              className={inputCls}
            />
          )}
        </Field>
        <div className="col-span-2 flex items-center gap-3">
          <button type="submit" className={btnPrimary}>
            Save deal
          </button>
          <span role="status" className="text-[13px] text-muted">
            {msg}
          </span>
          {contact && (
            <Link
              href={`/w/${ws}/crm/contacts/${contact.id}`}
              className="ml-auto text-[13px] hover:underline"
            >
              Open {contact.name}
            </Link>
          )}
        </div>
      </form>
    </div>
  )
}
