import type { Metadata } from 'next'
import './globals.css'

export const metadata: Metadata = {
  title: 'C Note',
  description: 'Notes, projects, CRM and invoices on one model',
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  )
}
