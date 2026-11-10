import {
  BookOpen,
  FileText,
  FolderKanban,
  Home,
  type LucideIcon,
  Presentation,
  Receipt,
  Users,
} from 'lucide-react'

/** Module nav. Later waves replace `soon` with a real page by adding a route under app/w/[ws]/<key>. */
export type ModuleDef = { key: string; label: string; icon: LucideIcon; soon?: boolean }

export const MODULES: ModuleDef[] = [
  { key: '', label: 'Home', icon: Home },
  { key: 'docs', label: 'Docs', icon: BookOpen },
  { key: 'projects', label: 'Projects', icon: FolderKanban, soon: true },
  { key: 'crm', label: 'CRM', icon: Users, soon: true },
  { key: 'billing', label: 'Billing', icon: Receipt, soon: true },
  { key: 'whiteboard', label: 'Whiteboard', icon: Presentation },
]

export const PageIcon = FileText
