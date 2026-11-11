import { useLocalSearchParams } from 'expo-router'
import { ContactView } from '@/components/crm'
import { Screen } from '@/components/ui'

export default function ContactRoute() {
  const { id } = useLocalSearchParams<{ id: string }>()
  return (
    <Screen>
      <ContactView id={id} />
    </Screen>
  )
}
