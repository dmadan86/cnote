import { Suspense } from 'react'
import { ChatPage } from '@/components/chat/chat'

export default function Page() {
  return (
    <Suspense>
      <ChatPage />
    </Suspense>
  )
}
