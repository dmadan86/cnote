import { ShellView } from '@/components/shell'

export default async function Layout({
  params,
  children,
}: {
  params: Promise<{ ws: string }>
  children: React.ReactNode
}) {
  const { ws } = await params
  // HOME_WIDGETS is a runtime flag: the Setup guide menu item needs it to know the widget exists.
  return (
    <ShellView ws={ws} homeWidgets={process.env.HOME_WIDGETS === '1'}>
      {children}
    </ShellView>
  )
}
