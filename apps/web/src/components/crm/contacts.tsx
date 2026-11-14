'use client'
import { Plus, Search } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { type Contact, STATES, stateName } from '@/components/billing/fmt'
import { useShell } from '@/components/shell'
import { btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { cmd } from '@/lib/api'
import { useCmd } from '@/lib/hooks'

export function ContactForm({
  contact,
  onClose,
  onSaved,
}: {
  contact?: Contact
  onClose: () => void
  onSaved: (c: Contact) => void
}) {
  const { ws, spaces } = useShell()
  const [err, setErr] = useState<string>()
  const [vendor, setVendor] = useState<boolean>()
  const vendors = useCmd<{ id: string }[]>(ws, 'vendor.list', {})
  const isVendor = vendor ?? !!(contact && vendors.data?.some((v) => v.id === contact.id))
  const taxErr = err && /gstin|state/i.test(err)
  const text = (k: keyof Contact) => (contact?.[k] as string | null) ?? ''
  return (
    <Modal open title={contact ? 'Edit contact' : 'New contact'} onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={async (e) => {
          e.preventDefault()
          const f = Object.fromEntries(new FormData(e.currentTarget)) as Record<string, string>
          try {
            const c = contact
              ? await cmd<Contact>(ws, 'contact.update', { id: contact.id, ...f, is_vendor: vendor })
              : await cmd<Contact>(ws, 'contact.create', { space_id: spaces[0].id, ...f, is_vendor: vendor })
            onSaved(c)
          } catch (x) {
            setErr((x as Error).message)
          }
        }}
      >
        <Field label="Name">
          {(id) => <input id={id} name="name" required defaultValue={text('name')} className={inputCls} />}
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Type">
            {(id) => (
              <select
                id={id}
                name="kind"
                defaultValue={contact?.kind ?? 'company'}
                disabled={!!contact}
                className={inputCls}
              >
                <option value="company">Company</option>
                <option value="person">Person</option>
              </select>
            )}
          </Field>
          <Field label="Company">
            {(id) => <input id={id} name="company" defaultValue={text('company')} className={inputCls} />}
          </Field>
          <Field label="Email">
            {(id) => (
              <input id={id} name="email" type="email" defaultValue={text('email')} className={inputCls} />
            )}
          </Field>
          <Field label="Phone">
            {(id) => <input id={id} name="phone" defaultValue={text('phone')} className={inputCls} />}
          </Field>
          <Field label="GSTIN">
            {(id) => (
              <input
                id={id}
                name="gstin"
                defaultValue={text('gstin')}
                maxLength={15}
                autoCapitalize="characters"
                aria-invalid={taxErr || undefined}
                className={`${inputCls} uppercase ${taxErr ? 'border-bad' : ''}`}
              />
            )}
          </Field>
          <Field label="State">
            {(id) => (
              <select id={id} name="state_code" defaultValue={text('state_code')} className={inputCls}>
                <option value="">Auto (from GSTIN)</option>
                {STATES.map(([c, n]) => (
                  <option key={c} value={c}>
                    {c} {n}
                  </option>
                ))}
              </select>
            )}
          </Field>
        </div>
        <Field label="Billing address">
          {(id) => (
            <textarea
              id={id}
              name="billing_address"
              rows={2}
              defaultValue={text('billing_address')}
              className={inputCls}
            />
          )}
        </Field>
        <label className="flex items-center gap-2 text-[13px]">
          <input type="checkbox" checked={isVendor} onChange={(e) => setVendor(e.target.checked)} />
          This contact is a vendor (supplier)
        </label>
        <ErrorText error={err} />
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className={btnPrimary}>
            Save contact
          </button>
        </div>
      </form>
    </Modal>
  )
}

/** Shown when nothing can be created yet because the workspace has no space. */
export function NeedSpace() {
  const { openSpaceDialog } = useShell()
  return (
    <div className="rounded-md border border-dashed border-line p-6 text-center">
      <p className="mb-2 text-muted">Create a space first. Contacts, deals and invoices are stored in one.</p>
      <button type="button" className={btnPrimary} onClick={openSpaceDialog}>
        New space
      </button>
    </div>
  )
}

export function ContactsList() {
  const { ws, spaces } = useShell()
  const [q, setQ] = useState('')
  const [adding, setAdding] = useState(false)
  const { data, error, reload } = useCmd<Contact[]>(ws, 'contact.list', { q, limit: 500 })
  return (
    <div>
      <div className="mb-3 flex items-center gap-2">
        <label className="relative">
          <span className="sr-only">Search contacts</span>
          <Search size={14} className="absolute left-2.5 top-2.5 text-muted" />
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search contacts"
            className={`${inputCls} w-64 pl-8`}
          />
        </label>
        <button
          type="button"
          className={`${btnPrimary} ml-auto`}
          disabled={!spaces.length}
          onClick={() => setAdding(true)}
        >
          <Plus size={14} /> New contact
        </button>
      </div>
      <ErrorText error={error} />
      {!spaces.length && <NeedSpace />}
      {data?.length === 0 && spaces.length > 0 && (
        <p className="py-8 text-center text-muted">{q ? 'No contacts match.' : 'No contacts yet.'}</p>
      )}
      {!!data?.length && (
        <table className="w-full text-left text-[14px]">
          <thead className="text-[12px] text-muted">
            <tr className="border-b border-line">
              <th className="py-2 font-medium">Name</th>
              <th className="font-medium">Type</th>
              <th className="font-medium">Email</th>
              <th className="font-medium">GSTIN</th>
              <th className="font-medium">State</th>
            </tr>
          </thead>
          <tbody>
            {data.map((c) => (
              <tr key={c.id} className="border-b border-line hover:bg-hover">
                <td className="py-2 font-medium">
                  <Link href={`/w/${ws}/crm/contacts/${c.id}`} className="hover:underline">
                    {c.name}
                  </Link>
                </td>
                <td className="capitalize">{c.kind}</td>
                <td className="text-muted">{c.email}</td>
                <td className="font-mono text-[13px]">{c.gstin}</td>
                <td>{stateName(c.state_code)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && (
        <ContactForm
          onClose={() => setAdding(false)}
          onSaved={() => {
            setAdding(false)
            reload()
          }}
        />
      )}
    </div>
  )
}
