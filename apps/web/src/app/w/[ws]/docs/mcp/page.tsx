'use client'
import Link from 'next/link'
import { useEffect, useState } from 'react'
import { useShell } from '@/components/shell'

function Code({ label, children }: { label: string; children: string }) {
  return (
    <figure className="space-y-1">
      <figcaption className="text-[12px] font-medium text-muted">{label}</figcaption>
      <pre className="overflow-auto rounded-md bg-side p-3 text-[13px]">
        <code>{children}</code>
      </pre>
    </figure>
  )
}

export default function McpPage() {
  const { ws } = useShell()
  const [origin, setOrigin] = useState('')
  useEffect(() => setOrigin(window.location.origin), [])
  const url = `${origin}/mcp`
  const config = JSON.stringify(
    { mcpServers: { cnote: { url, headers: { Authorization: 'Bearer <API key>' } } } },
    null,
    2,
  )
  return (
    <div className="mx-auto max-w-3xl space-y-5 px-6 py-8">
      <h1 className="text-xl font-semibold">Connect an MCP client</h1>
      <p className="text-muted">
        Let Claude, Cursor or any MCP client read and write this workspace. Access follows the key's
        permissions.
      </p>
      <p>
        <Link href={`/w/${ws}/settings`} className="text-accent underline">
          Create an API key in Settings
        </Link>
        , then use it below.
      </p>
      <Code label="Endpoint (streamable HTTP)">{url}</Code>
      <Code label="Header">Authorization: Bearer &lt;API key&gt;</Code>
      <Code label="Claude / Cursor config">{config}</Code>
      <Code label="Local stdio alternative">CNOTE_API_KEY=&lt;API key&gt; cnote mcp-stdio</Code>
    </div>
  )
}
