'use client'
import { Download, FilePlus2, PlugZap } from 'lucide-react'
import Link from 'next/link'
import { useState } from 'react'
import { AskBox } from '@/components/docs/ask-box'
import { CaptureDialog } from '@/components/docs/capture-dialog'
import { Dashboard } from '@/components/docs/dashboard'
import { useShell } from '@/components/shell'
import { NewFromTemplateButton } from '@/components/templates/use-template'
import { btn } from '@/components/ui'

export default function DocsHome() {
  const { ws } = useShell()
  const [capture, setCapture] = useState(false)
  return (
    <div className="mx-auto max-w-3xl space-y-8 px-6 py-8">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-xl font-semibold">Docs</h1>
        <div className="ml-auto flex gap-2">
          <NewFromTemplateButton kind="page" />
          <button type="button" className={btn} onClick={() => setCapture(true)}>
            <FilePlus2 size={14} /> Capture
          </button>
          <Link href={`/w/${ws}/import`} className={btn}>
            <Download size={14} /> Import
          </Link>
          <Link href={`/w/${ws}/docs/mcp`} className={btn}>
            <PlugZap size={14} /> MCP
          </Link>
        </div>
      </div>
      <AskBox />
      <Dashboard />
      <CaptureDialog ws={ws} open={capture} onClose={() => setCapture(false)} />
    </div>
  )
}
