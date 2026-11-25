'use client'
import { ArrowDown, ArrowUp, Check, Copy, X } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { btn, btnPrimary, ErrorText, inputCls, Field as Labelled, Modal } from '@/components/ui'
import {
  createForm,
  disableForm,
  type Field,
  FORM_FIELD_TYPES,
  type FormConfig,
  listForms,
  rotateForm,
  type Table,
  type TableForm,
  updateForm,
} from '@/lib/tables'

const MAX_FORMS = 5
const when = (iso: string | null) =>
  iso ? new Date(iso).toLocaleString('en-IN', { dateStyle: 'medium', timeStyle: 'short' }) : 'never'

/** The one place a form's link is shown: the server keeps only its hash. */
function LinkOnce({ name, url, onDone }: { name: string; url: string; onDone: () => void }) {
  const full = new URL(url, window.location.origin).toString()
  const [copied, setCopied] = useState(false)
  return (
    <div role="status" className="space-y-2 rounded-md border border-accent p-3">
      <p className="text-[13px] font-medium">Link for {name}</p>
      <input
        readOnly
        aria-label="Form link"
        value={full}
        onFocus={(e) => e.target.select()}
        className={inputCls}
      />
      <p className="text-[13px]">Copy it now; you can make a new link later. It is not shown again.</p>
      <div className="flex gap-2">
        <button
          type="button"
          className={btnPrimary}
          onClick={() => navigator.clipboard.writeText(full).then(() => setCopied(true))}
        >
          {copied ? <Check size={14} /> : <Copy size={14} />} {copied ? 'Copied' : 'Copy link'}
        </button>
        <button type="button" className={btn} onClick={onDone}>
          Done
        </button>
      </div>
    </div>
  )
}

function Editor({
  ws,
  form,
  fields,
  onClose,
  onSaved,
}: {
  ws: string
  form: TableForm
  fields: Field[]
  onClose: () => void
  onSaved: () => void
}) {
  const eligible = fields.filter((f) => FORM_FIELD_TYPES.includes(f.type))
  const [name, setName] = useState(form.name)
  const [intro, setIntro] = useState(form.config.intro ?? '')
  const [thanks, setThanks] = useState(form.config.thank_you ?? '')
  const [rows, setRows] = useState(
    form.config.fields.filter((r) => eligible.some((f) => f.id === r.field_id)),
  )
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const by = (id: string) => fields.find((f) => f.id === id)
  const left = eligible.filter((f) => !rows.some((r) => r.field_id === f.id))
  const move = (i: number, d: number) =>
    setRows((r) => {
      const n = [...r]
      ;[n[i], n[i + d]] = [n[i + d], n[i]]
      return n
    })
  const set = (i: number, p: Partial<(typeof rows)[number]>) =>
    setRows((r) => r.map((x, j) => (j === i ? { ...x, ...p } : x)))

  async function save() {
    setErr(undefined)
    if (!name.trim()) return setErr('Give the form a name.')
    if (!rows.length) return setErr('Add at least one field to the form.')
    setBusy(true)
    try {
      const config: FormConfig = {
        ...form.config,
        intro: intro.trim(),
        thank_you: thanks.trim(),
        fields: rows.map((r) => ({ ...r, label: r.label?.trim() || undefined })),
      }
      await updateForm(ws, form.id, { name: name.trim(), config })
      onSaved()
    } catch (x) {
      setErr((x as Error).message)
      setBusy(false)
    }
  }

  const sm = 'min-h-11 min-w-11 sm:min-h-0 sm:min-w-0'
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        void save()
      }}
    >
      <Labelled label="Form name">
        {(id) => (
          <input id={id} className={inputCls} value={name} onChange={(e) => setName(e.target.value)} />
        )}
      </Labelled>
      <Labelled label="Intro text (shown above the questions)">
        {(id) => (
          <textarea
            id={id}
            rows={2}
            maxLength={2000}
            className={inputCls}
            value={intro}
            onChange={(e) => setIntro(e.target.value)}
          />
        )}
      </Labelled>
      <Labelled label="Thank-you text (shown after sending)">
        {(id) => (
          <textarea
            id={id}
            rows={2}
            maxLength={2000}
            className={inputCls}
            value={thanks}
            onChange={(e) => setThanks(e.target.value)}
          />
        )}
      </Labelled>

      <fieldset className="space-y-2">
        <legend className="text-[12px] font-medium text-muted">
          Questions, in the order people see them
        </legend>
        <ol className="space-y-2">
          {rows.map((r, i) => {
            const f = by(r.field_id)
            return (
              <li key={r.field_id} className="space-y-1.5 rounded-md border border-line p-2">
                <div className="flex items-center gap-1">
                  <span className="min-w-0 flex-1 truncate text-[13px] font-medium">{f?.name}</span>
                  <button
                    type="button"
                    aria-label={`Move ${f?.name} up`}
                    disabled={i === 0}
                    onClick={() => move(i, -1)}
                    className={`${sm} flex items-center justify-center rounded hover:bg-hover disabled:opacity-40`}
                  >
                    <ArrowUp size={14} />
                  </button>
                  <button
                    type="button"
                    aria-label={`Move ${f?.name} down`}
                    disabled={i === rows.length - 1}
                    onClick={() => move(i, 1)}
                    className={`${sm} flex items-center justify-center rounded hover:bg-hover disabled:opacity-40`}
                  >
                    <ArrowDown size={14} />
                  </button>
                  <button
                    type="button"
                    aria-label={`Remove ${f?.name} from the form`}
                    onClick={() => setRows((x) => x.filter((_, j) => j !== i))}
                    className={`${sm} flex items-center justify-center rounded hover:bg-hover`}
                  >
                    <X size={14} />
                  </button>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <label className="min-w-0 flex-1 text-[12px] text-muted">
                    <span className="sr-only">Label for {f?.name}</span>
                    <input
                      aria-label={`Label for ${f?.name}`}
                      placeholder={f?.name}
                      maxLength={100}
                      value={r.label ?? ''}
                      onChange={(e) => set(i, { label: e.target.value })}
                      className={inputCls}
                    />
                  </label>
                  <label className="flex min-h-11 items-center gap-1.5 text-[13px] sm:min-h-0">
                    <input
                      type="checkbox"
                      checked={!!r.required}
                      onChange={(e) => set(i, { required: e.target.checked })}
                    />
                    Required
                  </label>
                </div>
              </li>
            )
          })}
        </ol>
        {left.length > 0 && (
          <label className="flex items-center gap-2 text-[13px]">
            Add a field
            <select
              value=""
              onChange={(e) => e.target.value && setRows((r) => [...r, { field_id: e.target.value }])}
              className={`${inputCls} w-auto`}
            >
              <option value="">Choose a field</option>
              {left.map((f) => (
                <option key={f.id} value={f.id}>
                  {f.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <p className="text-[12px] text-muted">
          Forms cannot take file uploads, so attachment fields are not offered.
        </p>
      </fieldset>

      <ErrorText error={err} />
      <div className="flex justify-end gap-2">
        <button type="button" className={btn} onClick={onClose}>
          Cancel
        </button>
        <button type="submit" className={btnPrimary} disabled={busy}>
          Save form
        </button>
      </div>
    </form>
  )
}

/** Manager's dialog: the table's forms, their links, and the editor (design 18.3, revised by 18.8). */
export function FormBuilder({
  ws,
  table,
  fields,
  onClose,
}: {
  ws: string
  table: Table
  fields: Field[]
  onClose: () => void
}) {
  const [forms, setForms] = useState<TableForm[]>()
  const [err, setErr] = useState<string>()
  const [link, setLink] = useState<{ name: string; url: string }>()
  const [editing, setEditing] = useState<TableForm>()
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [confirm, setConfirm] = useState<{ id: string; what: 'rotate' | 'disable' }>()
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    listForms(ws, table.id).then(setForms, (x) => setErr((x as Error).message))
  }, [ws, table.id])
  useEffect(load, [load])

  const run = async (fn: () => Promise<void>) => {
    setErr(undefined)
    setBusy(true)
    try {
      await fn()
      load()
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
      setConfirm(undefined)
    }
  }
  const eligible = fields.filter((f) => FORM_FIELD_TYPES.includes(f.type))
  const create = () =>
    run(async () => {
      const start = eligible.filter((f) => !f.locked).slice(0, 50)
      const picked = start.length ? start : eligible.slice(0, 1)
      const { form, url } = await createForm(ws, table.id, newName.trim(), {
        fields: picked.map((f) => ({ field_id: f.id, required: f.id === table.primary_field_id })),
      })
      setLink({ name: form.name, url })
      setCreating(false)
      setNewName('')
    })
  const rotate = (f: TableForm) =>
    run(async () => {
      const { form, url } = await rotateForm(ws, f.id)
      setLink({ name: form.name, url })
    })

  return (
    <Modal open onClose={onClose} title="Forms" className="max-w-2xl">
      <div className="space-y-4">
        <h2 className="text-[15px] font-semibold">Forms for {table.name}</h2>
        {editing ? (
          <Editor
            ws={ws}
            form={editing}
            fields={fields}
            onClose={() => setEditing(undefined)}
            onSaved={() => {
              setEditing(undefined)
              load()
            }}
          />
        ) : (
          <>
            <p className="text-[13px] text-muted">
              Anyone with a form's link can add a record to this table, with no login. Forms take no file
              uploads.
            </p>
            {link && <LinkOnce name={link.name} url={link.url} onDone={() => setLink(undefined)} />}
            <ErrorText error={err} />
            {!forms && !err && <p className="text-[13px] text-muted">Loading forms.</p>}
            {forms?.length === 0 && <p className="text-[13px] text-muted">No forms yet.</p>}
            <ul aria-label="Forms" className="space-y-2">
              {forms?.map((f) => (
                <li key={f.id} className="space-y-2 rounded-md border border-line p-2.5">
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                    <span className="text-[14px] font-medium">{f.name}</span>
                    <span
                      className={`rounded px-1.5 py-0.5 text-[12px] ${f.enabled ? 'bg-ok-bg text-ok' : 'bg-hover text-muted'}`}
                    >
                      {f.enabled ? 'On' : 'Off'}
                    </span>
                    <span className="text-[12px] text-muted">
                      {f.submissions} {f.submissions === 1 ? 'response' : 'responses'}, last{' '}
                      {when(f.last_submission_at)}
                    </span>
                  </div>
                  {confirm?.id === f.id ? (
                    <div
                      role="alertdialog"
                      aria-label={`${confirm.what === 'rotate' ? 'Make a new link' : 'Turn off'} ${f.name}`}
                      className="space-y-2"
                    >
                      <p className="text-[13px]">
                        {confirm.what === 'rotate'
                          ? 'Make a new link? The old link stops working.'
                          : 'Turn this form off? Its link stops working. Records already sent stay.'}
                      </p>
                      <div className="flex gap-2">
                        <button type="button" className={btn} onClick={() => setConfirm(undefined)}>
                          Keep as is
                        </button>
                        <button
                          type="button"
                          className={`${btn} text-bad`}
                          disabled={busy}
                          onClick={() =>
                            confirm.what === 'rotate'
                              ? rotate(f)
                              : run(async () => void (await disableForm(ws, f.id)))
                          }
                        >
                          {confirm.what === 'rotate' ? 'Make new link' : 'Turn off'}
                        </button>
                      </div>
                    </div>
                  ) : (
                    <div className="flex flex-wrap gap-2">
                      <button
                        type="button"
                        className={`${btn} min-h-11 sm:min-h-0`}
                        onClick={() => setEditing(f)}
                      >
                        Edit
                      </button>
                      {f.enabled ? (
                        <>
                          <button
                            type="button"
                            className={`${btn} min-h-11 sm:min-h-0`}
                            onClick={() => setConfirm({ id: f.id, what: 'rotate' })}
                          >
                            New link
                          </button>
                          <button
                            type="button"
                            className={`${btn} min-h-11 sm:min-h-0`}
                            onClick={() => setConfirm({ id: f.id, what: 'disable' })}
                          >
                            Turn off
                          </button>
                        </>
                      ) : (
                        <button
                          type="button"
                          className={`${btn} min-h-11 sm:min-h-0`}
                          disabled={busy}
                          onClick={() => rotate(f)}
                        >
                          Turn on with a new link
                        </button>
                      )}
                    </div>
                  )}
                </li>
              ))}
            </ul>
            {creating ? (
              <form
                className="space-y-2"
                onSubmit={(e) => {
                  e.preventDefault()
                  if (newName.trim()) void create()
                }}
              >
                <Labelled label="Form name">
                  {(id) => (
                    <input
                      id={id}
                      className={inputCls}
                      value={newName}
                      onChange={(e) => setNewName(e.target.value)}
                    />
                  )}
                </Labelled>
                <div className="flex gap-2">
                  <button type="button" className={btn} onClick={() => setCreating(false)}>
                    Cancel
                  </button>
                  <button type="submit" className={btnPrimary} disabled={busy || !newName.trim()}>
                    Create form
                  </button>
                </div>
              </form>
            ) : (
              <div className="flex items-center gap-3">
                <button
                  type="button"
                  className={btnPrimary}
                  disabled={!forms || forms.length >= MAX_FORMS}
                  onClick={() => setCreating(true)}
                >
                  New form
                </button>
                {forms && forms.length >= MAX_FORMS && (
                  <span className="text-[12px] text-muted">A table can have up to {MAX_FORMS} forms.</span>
                )}
              </div>
            )}
            <div className="flex justify-end">
              <button type="button" className={btn} onClick={onClose}>
                Close
              </button>
            </div>
          </>
        )}
      </div>
    </Modal>
  )
}
