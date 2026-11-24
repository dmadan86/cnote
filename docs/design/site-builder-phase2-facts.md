# Site builder phase 2: verified facts (Pexels, registrar DNS)

Researched 2026-11-24 from live pages. Each claim is tagged:
**[V]** read on the vendor's own page today, **[S]** seen only in a search snippet or third-party guide, **[U]** not verified.

## 1. Pexels

### Blockers to know first

- **New API keys are paused.** "New API key issuance is currently paused", with no end date. [V] ([API key article](https://help.pexels.com/hc/en-us/articles/900004904026-How-do-I-get-an-API-key), [API page](https://www.pexels.com/api/)). If we do not already hold a key, we cannot ship this integration now.
- **Rate limit increases are on hold.** [V] ([unlimited requests article](https://help.pexels.com/hc/en-us/articles/900005852323-How-do-I-get-unlimited-requests)). Plan for the default limits.
- One key per Pexels user account. [V]

### Can a SaaS site builder let users search Pexels and copy chosen photos into its own storage for their published sites?

**Probably yes, but no Pexels page says so in those words.** What the pages do say:

- The licence lets anyone "download, use, copy, modify or adapt the Content for commercial or non-commercial purposes". [V] ([Terms of Service](https://www.pexels.com/terms-of-service/) section 5, last updated 20 October 2026)
- The same terms apply to API users as to website users. [V] ([API terms article](https://help.pexels.com/hc/en-us/articles/900005880463))
- An integration must "add a feature or build on an existing app or website that provides value beyond just our API". A site builder with a photo picker fits this. [V]
- **Nothing found on caching, hotlinking, or storing copies**, in the API docs, the API help category, or the Terms. [V that the pages are silent] Copying the chosen photo to our storage rests on the general "download, copy" grant. Get this confirmed by Pexels or legal before launch. [U]
- If a rights claim arises we must "immediately delete or remove the Content" from our storage (Terms section 11). [V] So we need to be able to find and remove a stored Pexels photo by its Pexels ID.

### Attribution

| Where | Rule | Status |
|---|---|---|
| User's published site | Not required. "You do not need to credit Pexels or the contributor". Appreciated. | [V] Terms section 6, [License](https://www.pexels.com/license/) |
| Our app UI (the picker) | Expected. "Whenever you are doing an API request make sure to show a prominent link to Pexels." "Always credit our photographers when possible." | [V] [API docs](https://www.pexels.com/api/documentation/) |
| To get limits lifted | Required. "Pexels and our contributors must be credited within your platform", shown by mock-up or demo. | [V] |

Credit wording, all from Pexels pages [V] ([credit article](https://help.pexels.com/hc/en-us/articles/900005851903-How-should-I-give-credit-Can-I-use-your-logo)):

- Picker footer: `Photos provided by Pexels`, linked to `https://www.pexels.com`. A linked Pexels logo (black or white PNG) is also accepted.
- Per photo: `Photo by John Doe on Pexels`, linked to the photo's Pexels page (the `url` field; photographer is `photographer` / `photographer_url`).
- Fallback if a full link is not possible: `Powered by Pexels`, linked to pexels.com.
- Optional credit on a published site: `by [Contributor] via Pexels`.
- Do not use the Pexels logo as an app icon.

### Limits and restrictions

- **Default rate limit: 200 requests per hour and 20,000 per month.** [V] Working around the limit ends API access. [V]
- No copying Pexels' core functionality. Wallpaper apps are excluded. [V]
- No similar or competing service built from Pexels content. [V] Terms section 8
- "Bulk, large-scale or systematic copying of Content is strictly prohibited" without permission. [V] So copy only the photos a user picks. Never pre-fetch or mirror search results.
- No scraping, datasets, or ML/AI training through the API without permission. [V]
- No selling or distributing photos "Standalone" (unchanged, as the product itself). Cropping, resizing, or a filter still counts as Standalone. [V] A photo placed on a web page with other content is a new work by the Terms' own test, but we must not offer a "download this photo" feature.
- Users must not show identifiable people in a bad light, imply endorsement, or use photos in a trademark or logo. [V] Worth one line in picker help text.

## 2. Registrar DNS steps

Target setup: `www` CNAME to our host, apex either an A record to our IP or a redirect to `www`, plus one TXT for verification. Menu labels below are the vendors' own unless tagged.

### GoDaddy India
GoDaddy help pages block automated reads; steps are from search snippets of the official articles. [S]
- Records: Sign in > **Domain Portfolio** > select the domain > **DNS** > **Add New Record** > choose **Type** (CNAME, A, TXT) > **Name**, **Value**, **TTL** > **Save**. ([Add a CNAME record](https://www.godaddy.com/en-in/help/add-a-cname-record-19236))
- Name is `www` for the CNAME, `@` for the apex. Default TTL is 1 hour.
- Apex redirect: **DNS** > **Forwarding** > **Add Forwarding** > destination `https://www.example.com` > **Permanent (301)** > **Save**. ([Forward my domain](https://www.godaddy.com/en-in/help/forward-my-godaddy-domain-12123))
- Pitfall: forwarding "will automatically update and lock your @ A record". Delete forwarding before editing that record. HTTPS on forwarding is automatic.
- "Most DNS changes take effect within an hour but could take up to 48 hours."

### BigRock
BigRock help pages also block automated reads. Labels below are the least certain in this document. [S]
- Records: sign in at `myorders.bigrock.in` > **Orders** > click the domain > **DNS Management** > **Manage DNS** > pick the record tab > **Add A Record** / **Add CNAME Record** / **Add TXT Record**. ([BigRock DNS panel article](https://www.bigrock.in/help/article/How-to-add-DNS-records-under-the-BigRock-DNS-panel), [third-party walkthrough](https://help.vacationlabs.com/portal/en/kb/articles/adding-a-record-and-cname-in-bigrock))
- Works only if the domain uses BigRock nameservers. Premium DNS, cPanel and Plesk orders have separate panels.
- Apex redirect: domain page > **Domain Forwarding** > **Manage** (or **Enable**) > enter destination > **Save**. Needs BigRock nameservers. ([Set up domain forwarding](https://www.bigrock.in/help/article/Set-Up-Domain-Forwarding-in-BigRock))
- Leaving Host Name blank for the apex, and the default TTL: [U].
- Propagation: up to 24 to 48 hours.

### Hostinger India
- Records: hPanel > **Domains** > **DNS** > select the domain (or **Manage** > **DNS / Nameservers**) > **Manage DNS records** card > **Type**, **Name**, **Value**, **TTL** > **Add Record**. [V] ([Manage DNS records](https://www.hostinger.com/support/1583249-how-to-manage-dns-records-at-hostinger/))
- Default TTL 14400 seconds. Propagation "up to 24 hours". [V]
- Apex: choose type CNAME with Name `@`; Hostinger saves it as an **ALIAS**. [S] ([CNAME vs ALIAS](https://www.hostinger.com/support/10085192-cname-vs-alias-records-at-hostinger/))
- Pitfalls: delete any existing `www` A or CNAME record first. Hostinger CDN, if on, can break after a `www` change. The editor works only with Hostinger nameservers. [S]

### Namecheap
- Records: **Domain List** > **Manage** > **Advanced DNS** tab > **Host Records** > **Add New Record** > **Type**, **Host**, **Value**, **TTL** > **Save All Changes**. [V] ([Create a CNAME record](https://www.namecheap.com/support/knowledgebase/article.aspx/9646/2237/how-to-create-a-cname-record-for-your-domain/))
- Host is `www` only, never `www.example.com`. Value must be a full hostname.
- Apex: Namecheap advises against a CNAME on `@` because it can break email. Add a **URL Redirect Record** for `@` to `http://www.example.com/`. [V] Whether that redirect answers on HTTPS: [U].
- Pitfall: remove existing CNAME, URL Redirect, ALIAS or A records on the same Host first (new domains ship with a parking record). Allow about 30 minutes.

### Squarespace Domains (was Google Domains)
- Records: `account.squarespace.com/domains` > click the domain > **DNS** > **DNS Settings** > **Custom Records** > **Add record** > **Type**, **Host** (shown as **Name** when editing), **TTL**, **Data** > save. Path to DNS is [V]; the **Add record** label is [S]. ([Custom DNS records](https://support.squarespace.com/hc/en-us/articles/360002101888-Adding-custom-DNS-records-to-your-Squarespace-managed-domain))
- Editing a record asks for the account password or 2FA again. [V]
- Default TTL is 4 hours. Changes "can take 24 to 48 hours". [V]
- Apex: **ALIAS** type with Host `@` is supported. [S] Forwarding `@` to `www` exists, but third-party reports call it unreliable for sites hosted elsewhere. [S] Prefer ALIAS or an A record.
- Pitfall: default Squarespace records and forwarding rules conflict with new ones and raise an error. Remove them first. [V]

### Cloudflare
- Records: dashboard > select the domain > **DNS** > **Records** > **Add record** > **Type**, **Name**, target, **Proxy status**, **TTL** > **Save**. [V] ([Create DNS records](https://developers.cloudflare.com/dns/manage-dns-records/how-to/create-dns-records/))
- Apex: Name `@`. A CNAME on `@` is allowed, because Cloudflare flattens it to an IP. No forwarding needed. [V] ([Zone apex](https://developers.cloudflare.com/dns/manage-dns-records/how-to/create-zone-apex/))
- **Biggest pitfall: set Proxy status to DNS only (grey cloud)** on both records. Cloudflare lists "SaaS-hosted websites" and domain verification records as cases that should be DNS only. [V] ([Proxy status](https://developers.cloudflare.com/dns/proxy-status/)) Proxied (orange cloud) hides our host from the visitor, so our certificate issuance and domain check can fail. The exact ACME failure mode is our inference, not Cloudflare's wording. [U]
- TTL: Auto is 300 seconds. DNS-only records allow 60 seconds to 1 day. [V] ([TTL](https://developers.cloudflare.com/dns/manage-dns-records/reference/ttl/))
- TXT records are always DNS only. [V]

### Pitfalls for every registrar (help copy)

1. **Old records block new ones.** Delete the existing `@` A record, parking record, and any `www` record before adding ours.
2. **No CNAME on the bare domain**, except Cloudflare, Hostinger (ALIAS) and Squarespace (ALIAS). Elsewhere use our A record or forward the bare domain to `www`.
3. **Type only the short name**: `www` or `@`, not the full domain.
4. **Wrong panel.** The DNS editor only works where the nameservers point. A domain bought at GoDaddy but using Cloudflare nameservers must be edited at Cloudflare.
5. **Waiting.** Usually under an hour, up to 48 hours. Lowering TTL only speeds up later changes.
6. **TXT host.** Verification TXT goes on `@` unless we give a prefix. A domain may hold only one SPF TXT record, so never ask users to replace an existing TXT.
7. **Registrar forwarding and HTTPS.** Only GoDaddy states its forwarding handles HTTPS. For the others it is unverified, so an A record to our IP is the safer apex option. [U]

## Not verified

- GoDaddy and BigRock labels were not read on the vendor pages (HTTP 403). Check both in a real account before writing help copy.
- No India-specific panel differences were confirmed for GoDaddy or Hostinger; the `en-in` help pages carry the same steps as the global ones. [S]
- Pexels has no separate "API Terms" document that we could find. The API guidelines in the docs plus the general Terms are the terms.
- One search snippet dated the Pexels Terms 15 November 2024; the live page says 20 October 2026. The live page is used here.
