'use client'
import { MyTasks } from '@/components/docs/my-tasks'
import { useShell } from '@/components/shell'

export default function TasksPage() {
  const { ws } = useShell()
  return <MyTasks ws={ws} />
}
