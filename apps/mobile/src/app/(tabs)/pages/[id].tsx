import { useLocalSearchParams } from 'expo-router'
import { PageEditor } from '@/components/pages'
import { Screen } from '@/components/ui'

export default function PageRoute() {
  const { id } = useLocalSearchParams<{ id: string }>()
  return (
    <Screen>
      <PageEditor id={id} />
    </Screen>
  )
}
