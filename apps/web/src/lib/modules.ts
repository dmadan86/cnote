import {
  BarChart3,
  BookOpen,
  FileText,
  FolderKanban,
  Globe,
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
  { key: 'projects', label: 'Projects', icon: FolderKanban },
  { key: 'crm', label: 'CRM', icon: Users },
  { key: 'billing', label: 'Billing', icon: Receipt },
  { key: 'reports', label: 'Reports', icon: BarChart3 },
  { key: 'whiteboard', label: 'Whiteboard', icon: Presentation },
  { key: 'site', label: 'Website', icon: Globe },
]

/** settings.modules: missing = all enabled. Home and Reports are always on (Reports hides datasets of switched-off modules itself). */
export const moduleOn = (enabled: string[] | null | undefined, key: string) =>
  !key || key === 'reports' || !enabled || enabled.includes(key)

export const PageIcon = FileText
