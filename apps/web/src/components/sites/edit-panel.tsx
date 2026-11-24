'use client'
import { Puck } from '@puckeditor/core'
import type { Site, SiteData } from '@/lib/sites'
import { SiteEditor } from './editor'

/** The Edit tab: B1's editor in the left column (add, outline, fields); the preview stays in the builder's centre. */
export function EditTab(props: {
  ws: string
  siteId: string
  page: string
  versionId: string
  data: SiteData
  images: { id: string; filename: string }[]
  readOnly: boolean
  onSaved: (r: { site: Site; data: SiteData }) => void
  onReload: () => void
}) {
  return (
    <SiteEditor {...props} isHome={!props.page} assetBase={`/api/w/${props.ws}/sites/${props.siteId}/img`}>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
        <details>
          <summary className="cursor-pointer text-sm font-semibold">Add a section</summary>
          <Puck.Components />
        </details>
        <Puck.Outline />
        <Puck.Fields />
      </div>
    </SiteEditor>
  )
}
