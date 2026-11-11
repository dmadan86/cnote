import { useState } from 'react'
import { KeyboardAvoidingView, Platform, ScrollView, Text, View } from 'react-native'
import { SafeAreaView } from 'react-native-safe-area-context'
import { Button, Field, Label } from '@/components/ui'
import { errMsg } from '@/lib/hooks'
import { useSession } from '@/lib/session'
import { useTheme } from '@/lib/theme'

export default function SignIn() {
  const t = useTheme()
  const { server, signIn, signUp } = useSession()
  const [url, setUrl] = useState(server)
  const [mode, setMode] = useState<'in' | 'up'>('in')
  const [name, setName] = useState('')
  const [workspace, setWorkspace] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const up = mode === 'up'
  const ready =
    !!url.trim() && !!email.trim() && password.length > 0 && (!up || (!!name.trim() && !!workspace.trim()))

  async function submit() {
    setBusy(true)
    setError(null)
    try {
      if (up)
        await signUp(url, {
          name: name.trim(),
          email: email.trim(),
          password,
          workspace_name: workspace.trim(),
        })
      else await signIn(url, email.trim(), password)
    } catch (e) {
      setError(errMsg(e))
      setBusy(false)
    }
  }

  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: t.canvas }}>
      <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
        <ScrollView
          contentContainerStyle={{ flexGrow: 1, justifyContent: 'center', alignItems: 'center', padding: 24 }}
          keyboardShouldPersistTaps="handled"
        >
          <View style={{ width: '100%', maxWidth: 420, gap: 12 }}>
            <Text style={{ color: t.fg, fontSize: 32, fontWeight: '700' }} accessibilityRole="header">
              C Note
            </Text>
            <Label muted>{up ? 'Create your account and workspace' : 'Sign in to your workspace'}</Label>
            <Field
              label="Server URL"
              value={url}
              onChangeText={setUrl}
              keyboardType="url"
              autoCorrect={false}
            />
            {up ? (
              <>
                <Field
                  label="Your name"
                  value={name}
                  onChangeText={setName}
                  autoCapitalize="words"
                  textContentType="name"
                />
                <Field
                  label="Workspace name"
                  value={workspace}
                  onChangeText={setWorkspace}
                  autoCapitalize="words"
                />
              </>
            ) : null}
            <Field
              label="Email"
              value={email}
              onChangeText={setEmail}
              keyboardType="email-address"
              autoCorrect={false}
              textContentType="username"
            />
            <Field
              label="Password"
              value={password}
              onChangeText={setPassword}
              secureTextEntry
              textContentType={up ? 'newPassword' : 'password'}
              onSubmitEditing={() => ready && submit()}
            />
            {error ? (
              <Text accessibilityRole="alert" style={{ color: t.bad }}>
                {error}
              </Text>
            ) : null}
            <Button
              title={up ? 'Create account' : 'Sign in'}
              onPress={submit}
              busy={busy}
              disabled={!ready}
            />
            <Button
              title={up ? 'I already have an account' : 'Create an account'}
              kind="plain"
              onPress={() => {
                setMode(up ? 'in' : 'up')
                setError(null)
              }}
            />
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  )
}
