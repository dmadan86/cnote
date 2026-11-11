import { useLocalSearchParams } from 'expo-router'
import { ItemView } from '@/components/projects'
import { Screen } from '@/components/ui'

export default function ItemRoute() {
  const { id } = useLocalSearchParams<{ id: string }>()
  return (
    <Screen>
      <ItemView id={id} />
    </Screen>
  )
}
