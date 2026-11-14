'use client'
import { SubNav } from '@/components/billing/bits'
import { useShell } from '@/components/shell'

export default function BillingLayout({ children }: { children: React.ReactNode }) {
  const { ws } = useShell()
  return (
    <div className="px-6 py-6">
      <h1 className="mb-3 text-xl font-semibold">Billing</h1>
      <SubNav
        label="Billing sections"
        items={[
          [`/w/${ws}/billing`, 'Invoices'],
          [`/w/${ws}/billing/quotes`, 'Quotes'],
          [`/w/${ws}/billing/bills`, 'Bills'],
          [`/w/${ws}/billing/expenses`, 'Expenses'],
          [`/w/${ws}/billing/recurring`, 'Recurring'],
          [`/w/${ws}/billing/reports`, 'Reports'],
          [`/w/${ws}/billing/settings`, 'Settings'],
        ]}
      />
      {children}
    </div>
  )
}
