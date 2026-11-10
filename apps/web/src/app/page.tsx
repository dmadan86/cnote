'use client'
import { useRouter } from 'next/navigation'
import { useEffect } from 'react'
import { auth } from '@/lib/api'

export default function Root() {
  const router = useRouter()
  useEffect(() => {
    auth
      .me()
      .then((m) => router.replace(m.workspaces[0] ? `/w/${m.workspaces[0].id}` : '/signup'))
      .catch(() => router.replace('/login'))
  }, [router])
  return null
}
