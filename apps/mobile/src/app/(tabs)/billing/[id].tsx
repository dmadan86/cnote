import { useLocalSearchParams } from 'expo-router'
import { InvoiceView } from '@/components/billing'
import { Screen } from '@/components/ui'

export default function InvoiceRoute() {
  const { id } = useLocalSearchParams<{ id: string }>()
  return (
    <Screen>
      <InvoiceView id={id} />
    </Screen>
  )
}
