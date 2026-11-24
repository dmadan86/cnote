import { Review } from './review'

export default function Page() {
  return (
    <main className="mx-auto max-w-4xl space-y-4 p-4 sm:p-8">
      <h1 className="text-xl font-semibold">Website review</h1>
      <Review />
    </main>
  )
}
