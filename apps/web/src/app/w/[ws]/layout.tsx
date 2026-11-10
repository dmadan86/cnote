import { ShellView } from '@/components/shell'

export default async function Layout({
  params,
  children,
}: {
  params: Promise<{ ws: string }>
  children: React.ReactNode
}) {
  const { ws } = await params
  return <ShellView ws={ws}>{children}</ShellView>
}
