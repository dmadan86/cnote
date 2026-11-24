'use client'
import { useEffect } from 'react'
import { externalReferrerHost } from '@/lib/sites'

/**
 * Counts one visit per page view, without cookies (design 17.8): a text/plain beacon of {path, ref_host} only,
 * the referrer's host and never when it is this site itself. Failures are dropped; nothing waits on the answer.
 */
export function Beacon({ siteId }: { siteId: string }) {
  useEffect(() => {
    if (window.top !== window) return // framed (e.g. a preview): not a visit
    try {
      const body = JSON.stringify({
        path: location.pathname,
        ref_host: externalReferrerHost(document.referrer, location.host),
      })
      navigator.sendBeacon(`/api/sites/hit/${siteId}`, new Blob([body], { type: 'text/plain' }))
    } catch {
      // analytics never break the page
    }
  }, [siteId])
  return null
}
