import { MfaCard } from '@/components/security/mfa'

export default function Page() {
  return (
    <main className="mx-auto max-w-xl space-y-6 p-4 sm:p-8">
      <h1 className="text-xl font-semibold">Account security</h1>
      <MfaCard />
    </main>
  )
}
