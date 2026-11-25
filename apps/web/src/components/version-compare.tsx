'use client'
import { diffWords } from 'diff'

/**
 * Inline word diff of two versions of a page. Additions and removals are marked three ways, so colour is never the
 * only cue: an underline or a strike-through, the ins and del elements, and visually hidden "Added:" and "Removed:"
 * text that screen readers read. The diff is of the saved Markdown text, which is what a version stores.
 */
export function VersionCompare({
  from,
  to,
  fromLabel,
  toLabel,
}: {
  from: string
  to: string
  fromLabel: string
  toLabel: string
}) {
  let end = 0 // character offset in the merged text: a stable key
  const parts = diffWords(from, to).map((p) => {
    end += p.value.length
    return { ...p, at: end }
  })
  const added = parts.filter((p) => p.added).length
  const removed = parts.filter((p) => p.removed).length
  return (
    <section aria-label={`Changes from ${fromLabel} to ${toLabel}`} className="space-y-1.5">
      <p role="status" className="text-[12px] text-muted">
        {added + removed === 0
          ? `No differences between ${fromLabel} and ${toLabel}.`
          : `${added} addition${added === 1 ? '' : 's'} and ${removed} removal${removed === 1 ? '' : 's'}, ${fromLabel} to ${toLabel}.`}
      </p>
      <div className="max-h-72 overflow-auto whitespace-pre-wrap rounded border border-line p-2 font-sans text-[12px]">
        {parts.map((p) =>
          p.added ? (
            <ins key={p.at} className="bg-ok-bg underline decoration-2">
              <span className="sr-only">Added: </span>
              {p.value}
              <span className="sr-only"> (end of addition)</span>
            </ins>
          ) : p.removed ? (
            <del key={p.at} className="bg-bad-bg line-through decoration-2">
              <span className="sr-only">Removed: </span>
              {p.value}
              <span className="sr-only"> (end of removal)</span>
            </del>
          ) : (
            <span key={p.at}>{p.value}</span>
          ),
        )}
      </div>
    </section>
  )
}
