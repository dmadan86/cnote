'use client'
import { use } from 'react'
import { Portal } from '@/components/portal'

export default function Page({ params }: { params: Promise<{ ws: string }> }) {
  return <Portal ws={use(params).ws} />
}
