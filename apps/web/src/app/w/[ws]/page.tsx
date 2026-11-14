'use client'
import { HomeDashboard } from '@/components/home/dashboard'
import { PageList } from '@/components/page-list'
import { useShell } from '@/components/shell'
import { btnPrimary } from '@/components/ui'

const REVIEW_WINDOW_MS = 14 * 86400_000

export default function Home() {
  const { me, notes, spaces, openSpaceDialog } = useShell()
  const pages = notes.filter((n) => n.types.length === 0)
  const needsReview = pages.filter(
    (n) =>
      n.owner_id === me.user.id &&
      (n.freshness === 'stale' ||
        n.freshness === 'unverified' ||
        (n.verify_by && new Date(n.verify_by).getTime() - Date.now() < REVIEW_WINDOW_MS)),
  )
  return (
    <div className="mx-auto max-w-4xl space-y-8 px-6 py-8">
      <h1 className="text-xl font-semibold">Welcome back, {me.user.name.split(' ')[0]}</h1>
      {spaces.length === 0 && (
        <div className="rounded-md border border-dashed border-line p-6 text-center">
          <p className="mb-3 text-muted">Create a space to start writing pages.</p>
          <button type="button" onClick={openSpaceDialog} className={btnPrimary}>
            Create space
          </button>
        </div>
      )}
      {spaces.length > 0 && <HomeDashboard />}
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
        <PageList pages={pages.slice(0, 8)} empty="No pages yet." />
      </section>
    </div>
  )
}
