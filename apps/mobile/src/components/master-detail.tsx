import { useRouter } from 'expo-router'
import { type ReactNode, useState } from 'react'
import { Text, useWindowDimensions, View } from 'react-native'
import { isSplit, listPaneWidth } from '@/lib/layout'
import { useTheme } from '@/lib/theme'

/**
 * Phone: tapping a row pushes `/{base}/{id}`. Tablet (>= 768dp): the list stays on the left and the detail renders
 * in the right pane, so there is no navigation. The same `detail` component backs the pushed route.
 */
export function MasterDetail({
  base,
  list,
  detail,
  placeholder,
}: {
  base: string
  list: (open: (id: string) => void, selected: string | null) => ReactNode
  detail: (id: string) => ReactNode
  placeholder: string
}) {
  const { width } = useWindowDimensions()
  const t = useTheme()
  const router = useRouter()
  const [sel, setSel] = useState<string | null>(null)
  const split = isSplit(width)
  const open = (id: string) => (split ? setSel(id) : router.push(`/${base}/${id}` as never))

  if (!split) return <>{list(open, null)}</>
  return (
    <View style={{ flex: 1, flexDirection: 'row', backgroundColor: t.canvas }}>
      <View style={{ width: listPaneWidth(width), borderRightWidth: 1, borderRightColor: t.line }}>
        {list(open, sel)}
      </View>
      <View style={{ flex: 1 }}>
        {sel ? (
          // key resets the detail's local state when another row is picked
          <View key={sel} style={{ flex: 1 }}>
            {detail(sel)}
          </View>
        ) : (
          <View style={{ flex: 1, alignItems: 'center', justifyContent: 'center' }}>
            <Text style={{ color: t.muted }}>{placeholder}</Text>
          </View>
        )}
      </View>
    </View>
  )
}
