'use client'
import { SubNav } from '@/components/billing/bits'
import { useShell } from '@/components/shell'

export default function CrmLayout({ children }: { children: React.ReactNode }) {
  const { ws } = useShell()
  return (
    <div className="px-6 py-6">
      <h1 className="mb-3 text-xl font-semibold">CRM</h1>
      <SubNav
        label="CRM sections"
        items={[
          [`/w/${ws}/crm`, 'Contacts'],
          [`/w/${ws}/crm/deals`, 'Deals'],
          [`/w/${ws}/crm/inbox`, 'Inbox'],
          [`/w/${ws}/crm/follow-ups`, 'Follow-ups'],
          [`/w/${ws}/crm/form`, 'Inquiry form'],
        ]}
      />
      {children}
    </div>
  )
}
