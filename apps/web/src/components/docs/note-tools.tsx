'use client'
import { diffWords } from 'diff'
import { BadgeCheck, Sparkles } from 'lucide-react'
import { useState } from 'react'
import { AiDialog, type AiMode } from '@/components/ai/ai-dialog'
import { Avatar, btn, btnPrimary, ErrorText, Field, inputCls, Modal } from '@/components/ui'
import { cmd, type Member, type NoteDetail } from '@/lib/api'
import { useCmd } from '@/lib/hooks'
import { CaptureDialog } from './capture-dialog'

const inDays = (n: number) => new Date(Date.now() + n * 864e5).toISOString().slice(0, 10)

/** Owner chip + change owner, Verify with next verify-by date, AI actions, capture. */
export function DocActions({
  ws,
  note,
  members,
  onChange,
}: {
  ws: string
  note: NoteDetail
  members: Member[]
  onChange: () => void
}) {
  const [dlg, setDlg] = useState<'verify' | 'owner' | 'capture' | AiMode | null>(null)
  const [menu, setMenu] = useState(false)
  const [err, setErr] = useState<string>()
  const owner = members.find((m) => m.user_id === note.owner_id)

  async function run(name: string, input: unknown) {
    try {
      await cmd(ws, name, input)
      setErr(undefined)
      setDlg(null)
      onChange()
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  const close = () => {
    setDlg(null)
    setErr(undefined)
  }

  return (
    <>
      <button
        type="button"
        onClick={() => setDlg('owner')}
        title="Change owner"
        className="flex items-center gap-1.5 rounded-full border border-line px-2 py-0.5 text-[12px] text-muted hover:bg-hover"
      >
        <Avatar name={owner?.name} size={18} /> {owner?.name ?? 'No owner'}
      </button>
      <button type="button" onClick={() => setDlg('verify')} className={btnPrimary}>
        <BadgeCheck size={14} /> Verify
      </button>
      <div className="relative">
        <button
          type="button"
          aria-haspopup="menu"
          aria-expanded={menu}
          onClick={() => setMenu(!menu)}
          onKeyDown={(e) => e.key === 'Escape' && setMenu(false)}
          className={btn}
        >
          <Sparkles size={14} /> AI
        </button>
        {menu && (
          <div
            role="menu"
            className="absolute right-0 z-20 mt-1 w-48 rounded-md border border-line bg-canvas p-1 shadow-lg"
          >
            {(
              [
                ['summarize', 'Summarize'],
                ['extract', 'Extract tasks'],
                ['draft', 'Draft with AI'],
                ['capture', 'Capture from text'],
              ] as const
            ).map(([k, label]) => (
              <button
                key={k}
                type="button"
                role="menuitem"
                onClick={() => {
                  setMenu(false)
                  setDlg(k)
                }}
                className="block w-full rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover"
              >
                {label}
              </button>
            ))}
          </div>
        )}
      </div>

      <Modal open={dlg === 'verify'} onClose={close} title="Verify page">
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            run('doc.verify', { note_id: note.id, verify_by: new FormData(e.currentTarget).get('by') })
          }}
        >
          <p className="text-[13px] text-muted">Confirm this page is still correct.</p>
          <Field label="Next verify-by date">
            {(id) => (
              <input
                id={id}
                name="by"
                type="date"
                required
                min={inDays(1)}
                defaultValue={inDays(90)}
                className={inputCls}
              />
            )}
          </Field>
          <ErrorText error={err} />
          <div className="flex justify-end gap-2">
            <button type="button" onClick={close} className={btn}>
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Confirm verified
            </button>
          </div>
        </form>
      </Modal>

      <Modal open={dlg === 'owner'} onClose={close} title="Change owner">
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            run('doc.set_owner', { note_id: note.id, owner_id: new FormData(e.currentTarget).get('owner') })
          }}
        >
          <Field label="Owner">
            {(id) => (
              <select id={id} name="owner" defaultValue={note.owner_id ?? ''} className={inputCls}>
                {members.map((m) => (
                  <option key={m.user_id} value={m.user_id}>
                    {m.name}
                  </option>
                ))}
              </select>
            )}
          </Field>
          <ErrorText error={err} />
          <div className="flex justify-end gap-2">
            <button type="button" onClick={close} className={btn}>
              Cancel
            </button>
            <button type="submit" className={btnPrimary}>
              Change owner
            </button>
          </div>
        </form>
      </Modal>

      <CaptureDialog ws={ws} open={dlg === 'capture'} onClose={close} defaultSpace={note.space_id} />
      {(dlg === 'summarize' || dlg === 'extract' || dlg === 'draft') && (
        <AiDialog ws={ws} mode={dlg} note={note} onClose={close} />
      )}
    </>
  )
}

type Proposal = { id: string; note_id: string; proposed_md: string; rationale: string; created_at: string }

/** AI-proposed edits are never applied automatically: show the word diff and let the owner decide. */
export function Proposals({ ws, note, onApplied }: { ws: string; note: NoteDetail; onApplied: () => void }) {
  const { data, reload } = useCmd<Proposal[]>(ws, 'doc.proposals', { note_id: note.id })
  const [err, setErr] = useState<string>()
  if (!data?.length) return null
  async function decide(p: Proposal, approve: boolean) {
    try {
      await cmd(ws, 'doc.proposal_decide', { proposal_id: p.id, approve })
      setErr(undefined)
      reload()
      if (approve) onApplied()
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  return (
    <div className="space-y-3 border-b border-line bg-side px-6 py-3">
      {data.map((p) => (
        <section
          key={p.id}
          aria-label="Proposed edit"
          className="rounded-md border border-line bg-canvas p-3"
        >
          <h2 className="text-[13px] font-semibold">Proposed edit</h2>
          {p.rationale && <p className="text-[12px] text-muted">{p.rationale}</p>}
          <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap font-sans text-[13px]">
            {diffWords(note.content_md, p.proposed_md).map((c, i) =>
              c.added ? (
                // biome-ignore lint/suspicious/noArrayIndexKey: static diff output
                <ins key={i} className="bg-ok-bg text-ok no-underline">
                  {c.value}
                </ins>
              ) : c.removed ? (
                // biome-ignore lint/suspicious/noArrayIndexKey: static diff output
                <del key={i} className="bg-bad-bg text-bad">
                  {c.value}
                </del>
              ) : (
                // biome-ignore lint/suspicious/noArrayIndexKey: static diff output
                <span key={i}>{c.value}</span>
              ),
            )}
          </pre>
          <div className="mt-2 flex items-center gap-2">
            <button type="button" className={btnPrimary} onClick={() => decide(p, true)}>
              Approve
            </button>
            <button type="button" className={btn} onClick={() => decide(p, false)}>
              Reject
            </button>
            <ErrorText error={err} />
          </div>
        </section>
      ))}
    </div>
  )
}
