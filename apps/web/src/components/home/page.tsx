'use client'
import { useMine } from '@/components/docs/page-header'
import { useRecentPages } from '@/components/docs/tree-loader'
import { HomeDashboard } from '@/components/home/dashboard'
import { SetupToast } from '@/components/home/setup-toast'
import { HomeWidgets } from '@/components/home/widgets-home'
import { PageList } from '@/components/page-list'
import { useShell } from '@/components/shell'
import { btnPrimary } from '@/components/ui'

const REVIEW_WINDOW_MS = 14 * 86400_000

export function HomePage({ widgets }: { widgets: boolean }) {
  const { ws, me, spaces, openSpaceDialog } = useShell()
  const notes = useRecentPages(ws, 500)
  const mine = useMine(ws, 'recent', 8)
  const pages = notes
  const needsReview = pages.filter(
    (n) =>
      n.owner_id === me.user.id &&
      (n.freshness === 'stale' ||
        n.freshness === 'unverified' ||
        (n.verify_by && new Date(n.verify_by).getTime() - Date.now() < REVIEW_WINDOW_MS)),
  )
  // Recently opened pages the shell can still list; a new person falls back to recently updated.
  const recent = mine.flatMap((r) => notes.filter((n) => n.id === r.id))
  return (
    <div className="mx-auto max-w-6xl space-y-8 px-6 py-8">
      <h1 className="text-2xl font-semibold tracking-tight">Welcome back, {me.user.name.split(' ')[0]}</h1>
      <SetupToast />
      {spaces.length === 0 && (
        <div className="rounded-md border border-dashed border-line p-6 text-center">
          <p className="mb-3 text-muted">Create a space to start writing pages.</p>
          <button type="button" onClick={openSpaceDialog} className={btnPrimary}>
            Create space
          </button>
        </div>
      )}
      {spaces.length > 0 && (widgets ? <HomeWidgets /> : <HomeDashboard />)}
      <section aria-labelledby="review">
        <h2 id="review" className="mb-2 text-[13px] font-semibold text-muted">
          Needs your review
        </h2>
        <PageList pages={needsReview} empty="Nothing needs review. Your pages are fresh." />
      </section>
      <section aria-labelledby="recent">
        <h2 id="recent" className="mb-2 text-[13px] font-semibold text-muted">
          Recent pages
        </h2>
        <PageList pages={recent.length ? recent : pages.slice(0, 8)} empty="No pages yet." />
      </section>
    </div>
  )
}
