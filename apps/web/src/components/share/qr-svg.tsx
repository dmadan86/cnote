'use client'
import { useMemo } from 'react'
import { qrMatrix, qrPath } from '@/lib/qr'

/** A QR code as inline SVG (dark modules on white, with its quiet zone, so it scans in dark mode and prints). */
export function QrSvg({
  text,
  label,
  ecc = 'M',
  size = 176,
}: {
  text: string
  label: string
  ecc?: 'L' | 'M'
  size?: number
}) {
  const q = useMemo(() => {
    try {
      return qrPath(qrMatrix(text, ecc))
    } catch {
      return null
    }
  }, [text, ecc])
  if (!q) return null
  return (
    <svg
      role="img"
      aria-label={label}
      viewBox={`0 0 ${q.size} ${q.size}`}
      width={size}
      height={size}
      shapeRendering="crispEdges"
      className="shrink-0 rounded bg-white"
    >
      <rect width={q.size} height={q.size} fill="#fff" />
      <path d={q.d} fill="#000" />
    </svg>
  )
}
