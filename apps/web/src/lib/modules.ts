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
  Table2,
  Users,
} from 'lucide-react'

/** Module nav. Later waves replace `soon` with a real page by adding a route under app/w/[ws]/<key>. */
export type ModuleDef = { key: string; label: string; icon: LucideIcon; soon?: boolean }

export const MODULES: ModuleDef[] = [
  { key: '', label: 'Home', icon: Home },
  { key: 'tables', label: 'Tables', icon: Table2 },
  { key: 'docs', label: 'Docs', icon: BookOpen },
  { key: 'projects', label: 'Projects', icon: FolderKanban },
  { key: 'crm', label: 'CRM', icon: Users },
  { key: 'billing', label: 'Billing', icon: Receipt },
  { key: 'reports', label: 'Reports', icon: BarChart3 },
  { key: 'whiteboard', label: 'Whiteboard', icon: Presentation },
  { key: 'site', label: 'Website', icon: Globe },
]

/**
 * `enabled` is every module except those the person uninstalled in the Marketplace (settings.hidden_modules, see the
 * shell). The old settings.modules list written by business-type presets hides nothing any more.
 */
export const moduleOn = (enabled: string[] | null | undefined, key: string) =>
  !key || key === 'reports' || !enabled || enabled.includes(key)

export const PageIcon = FileText
