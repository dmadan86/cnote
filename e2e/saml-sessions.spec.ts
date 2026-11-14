import { execFileSync } from 'node:child_process'
import { createHash, createSign, generateKeyPairSync, randomBytes, X509Certificate } from 'node:crypto'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { inflateRawSync } from 'node:zlib'
import { expect, test } from '@playwright/test'
import { newUser, signup } from './helpers'

test('the session list shows devices and revokes them', async ({ page, browser }) => {
  const u = await signup(page, newUser())
  // a second device signs in
  const other = await browser.newContext({
    userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/605.1.15',
  })
  const login = await other.request.post('/api/auth/login', {
    data: { email: u.email, password: u.password },
  })
  expect(login.ok()).toBeTruthy()

  await page.goto('/account/security')
  const rows = page.getByTestId('session-row')
  await expect(rows).toHaveCount(2) // this browser and the phone
  await expect(page.getByText('This device')).toHaveCount(1)
  await expect(page.getByText('Safari on iOS')).toBeVisible()
  await page.getByRole('button', { name: /^Revoke session: Safari on iOS/ }).click()
  await expect(page.getByText('Session revoked.')).toBeVisible()
  await expect(page.getByText('Safari on iOS')).toHaveCount(0)
  expect((await other.request.get('/api/auth/me')).status()).toBe(401)

  // sign out all others
  const again = await browser.newContext()
  await again.request.post('/api/auth/login', { data: { email: u.email, password: u.password } })
  await page.reload()
  page.once('dialog', (d) => d.accept())
  await page.getByRole('button', { name: 'Sign out all other devices' }).click()
  await expect(page.getByText(/Signed out \d+ other sessions?\./)).toBeVisible()
  await expect(page.getByTestId('session-row')).toHaveCount(1)
  expect((await again.request.get('/api/auth/me')).status()).toBe(401)
  expect((await page.request.get('/api/auth/me')).status()).toBe(200)
})

const NS_P = 'urn:oasis:names:tc:SAML:2.0:protocol'
const NS_A = 'urn:oasis:names:tc:SAML:2.0:assertion'
const sha = (s: string) => createHash('sha256').update(s).digest('base64')

/**
 * A SAML identity provider: metadata, an SSO endpoint that answers with an auto-submitting form carrying a signed
 * assertion. The assertion is written in exclusive-canonical form (sorted attributes, no whitespace) so the signature
 * is computed over exactly what is sent, with no XML library.
 */
async function fakeSamlIdp(email: string) {
  const { privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 })
  // a self-signed certificate is needed for the metadata: build one with openssl
  const dir = execFileSync('mktemp', ['-d']).toString().trim()
  const pem = privateKey.export({ type: 'pkcs8', format: 'pem' }).toString()
  execFileSync('sh', ['-c', `cat > ${dir}/k.pem`], { input: pem })
  execFileSync('openssl', [
    'req',
    '-new',
    '-x509',
    '-key',
    `${dir}/k.pem`,
    '-subj',
    '/CN=e2e-idp',
    '-days',
    '2',
    '-out',
    `${dir}/c.pem`,
  ])
  const certPem = execFileSync('cat', [`${dir}/c.pem`]).toString()
  const certB64 = new X509Certificate(certPem).raw.toString('base64')
  let base = ''
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', base)
    if (url.pathname === '/metadata') {
      res.setHeader('content-type', 'application/xml')
      return res.end(
        `<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="${base}/entity"><IDPSSODescriptor protocolSupportEnumeration="${NS_P}"><KeyDescriptor use="signing"><KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#"><X509Data><X509Certificate>${certB64}</X509Certificate></X509Data></KeyInfo></KeyDescriptor><SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="${base}/sso"/></IDPSSODescriptor></EntityDescriptor>`,
      )
    }
    if (url.pathname !== '/sso') {
      res.statusCode = 404
      return res.end()
    }
    const authn = inflateRawSync(Buffer.from(url.searchParams.get('SAMLRequest') ?? '', 'base64')).toString()
    const attr = (n: string) => new RegExp(`${n}="([^"]*)"`).exec(authn)?.[1] ?? ''
    const reqId = attr('ID')
    const acs = attr('AssertionConsumerServiceURL')
    const audience = /<saml:Issuer[^>]*>([^<]*)</.exec(authn)?.[1] ?? ''
    const now = new Date()
    const iso = (d: Date) => d.toISOString().replace(/\.\d+Z$/, 'Z')
    const aid = `_a${randomBytes(8).toString('hex')}`
    const body = (sig: string) =>
      `<saml:Issuer>${base}/entity</saml:Issuer>${sig}<saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified">e2e-${aid}</saml:NameID><saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData InResponseTo="${reqId}" NotOnOrAfter="${iso(new Date(now.getTime() + 300000))}" Recipient="${acs}"></saml:SubjectConfirmationData></saml:SubjectConfirmation></saml:Subject><saml:Conditions NotBefore="${iso(new Date(now.getTime() - 60000))}" NotOnOrAfter="${iso(new Date(now.getTime() + 300000))}"><saml:AudienceRestriction><saml:Audience>${audience}</saml:Audience></saml:AudienceRestriction></saml:Conditions><saml:AttributeStatement><saml:Attribute Name="email"><saml:AttributeValue>${email}</saml:AttributeValue></saml:Attribute><saml:Attribute Name="displayName"><saml:AttributeValue>E2E Saml</saml:AttributeValue></saml:Attribute></saml:AttributeStatement>`
    const open = `<saml:Assertion xmlns:saml="${NS_A}" ID="${aid}" IssueInstant="${iso(now)}" Version="2.0">`
    const digest = sha(`${open}${body('')}</saml:Assertion>`)
    const signedInfo = (ns: string) =>
      `<ds:SignedInfo${ns}><ds:CanonicalizationMethod Algorithm="http://www.w3.org/2001/10/xml-exc-c14n#"></ds:CanonicalizationMethod><ds:SignatureMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"></ds:SignatureMethod><ds:Reference URI="#${aid}"><ds:Transforms><ds:Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"></ds:Transform><ds:Transform Algorithm="http://www.w3.org/2001/10/xml-exc-c14n#"></ds:Transform></ds:Transforms><ds:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256"></ds:DigestMethod><ds:DigestValue>${digest}</ds:DigestValue></ds:Reference></ds:SignedInfo>`
    const sigValue = createSign('RSA-SHA256')
      .update(signedInfo(' xmlns:ds="http://www.w3.org/2000/09/xmldsig#"'))
      .sign(privateKey, 'base64')
    const sig = `<ds:Signature xmlns:ds="http://www.w3.org/2000/09/xmldsig#">${signedInfo('')}<ds:SignatureValue>${sigValue}</ds:SignatureValue><ds:KeyInfo><ds:X509Data><ds:X509Certificate>${certB64}</ds:X509Certificate></ds:X509Data></ds:KeyInfo></ds:Signature>`
    const response = `<samlp:Response xmlns:samlp="${NS_P}" xmlns:saml="${NS_A}" ID="_r${aid}" InResponseTo="${reqId}" IssueInstant="${iso(now)}" Destination="${acs}" Version="2.0"><saml:Issuer>${base}/entity</saml:Issuer><samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"></samlp:StatusCode></samlp:Status>${open}${body(sig)}</saml:Assertion></samlp:Response>`
    res.setHeader('content-type', 'text/html')
    res.end(
      `<form id="f" method="post" action="${acs}"><input type="hidden" name="SAMLResponse" value="${Buffer.from(response).toString('base64')}"></form><script>document.getElementById('f').submit()</script>`,
    )
  })
  return new Promise<{ base: string; close: () => void }>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
      resolve({ base, close: () => server.close() })
    })
  })
}

test('SAML single sign-on configured in settings, and role-gated controls', async ({ page, browser }) => {
  const id = randomBytes(4).toString('hex')
  const email = `saml-${id}@e2e-saml.test`
  const idp = await fakeSamlIdp(email)
  try {
    await signup(page, newUser())
    const me = await (await page.request.get('/api/auth/me')).json()
    const slug: string = me.workspaces[0].slug
    await page.getByRole('link', { name: 'Settings' }).first().click()
    await page.getByRole('tab', { name: 'Security' }).click()
    await page.getByLabel('Domain to add').fill('e2e-saml.test')
    await page.getByRole('button', { name: 'Add domain' }).click()
    await expect(page.getByTestId('txt-record')).toContainText('cnote-verification=')
    // no real DNS here: mark the domain verified in the database
    execFileSync(
      'psql',
      [
        process.env.DATABASE_ADMIN_URL ?? '',
        '-qc',
        `update core.sso_domains set verified_at = now() where domain = 'e2e-saml.test' and workspace_id = '${me.workspaces[0].id}'`,
      ],
      { stdio: 'pipe' },
    )
    await page.reload()
    await page.getByRole('tab', { name: 'Security' }).click()
    await page.getByLabel('SAML 2.0').check()
    await expect(page.getByRole('textbox', { name: 'Service provider metadata URL' })).toHaveValue(
      new RegExp(`/api/auth/saml/${slug}/metadata$`),
    )
    await expect(page.getByRole('textbox', { name: 'Assertion consumer service (ACS) URL' })).toHaveValue(
      new RegExp(`/api/auth/saml/${slug}/acs$`),
    )
    await page.getByLabel('Identity provider metadata URL').fill(`${idp.base}/metadata`)
    await page.getByLabel('Enable single sign-on').check()
    await page.getByRole('button', { name: 'Save single sign-on' }).click()
    await expect(page.getByText('Single sign-on settings saved.')).toBeVisible()
    await expect(page.getByText(`${idp.base}/entity`)).toBeVisible()

    // the service-provider metadata is what the IdP administrator imports
    const md = await page.request.get(`/api/auth/saml/${slug}/metadata`)
    expect(md.ok()).toBeTruthy()
    expect(await md.text()).toContain('WantAssertionsSigned="true"')

    // a member signs in through the IdP from a clean browser (the one sign-in entry serves both protocols)
    const ctx = await browser.newContext()
    const p2 = await ctx.newPage()
    await p2.goto('/login')
    await p2.getByRole('button', { name: 'Use company single sign-on' }).click()
    await p2.getByLabel('Workspace address').fill(slug)
    await p2.getByRole('button', { name: 'Continue' }).click()
    await expect(p2).toHaveURL(/\/w\/[0-9a-f-]+/)
    const me2 = await (await p2.request.get('/api/auth/me')).json()
    expect(me2.user.email).toBe(email)
    expect(me2.workspaces[0].role).toBe('member')

    // owner-only controls are not offered to a non-owner: no domain form, no SSO form, a read-only note instead
    await p2.getByRole('link', { name: 'Settings' }).first().click()
    await p2.getByRole('tab', { name: 'Security' }).click()
    await expect(p2.getByText(/Only workspace owners and admins can see sign-in security/)).toBeVisible()
    await expect(p2.getByRole('button', { name: 'Add domain' })).toHaveCount(0)
    await expect(p2.getByRole('button', { name: 'Save single sign-on' })).toHaveCount(0)
    await ctx.close()
  } finally {
    idp.close()
  }
})
