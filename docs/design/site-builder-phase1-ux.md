# Website builder: UX spec, Phase 1 (24 Nov 2026)

Builds on `site-builder-ux.md` (Phase 0: chat left 380px, preview right in a browser frame, change cards with Undo, block toolbar). Source: Mobbin screens and site sections studied on 24 Nov 2026. Every link is a screen or section that was actually viewed. Items marked **(inferred)** were not seen on Mobbin and are our own judgement. Numbers such as "30 edits" are placeholders until the plan is decided.

## 1. Publish

**References**
- [Buffer Start Page](https://mobbin.com/screens/3f609f34-47ea-421c-8732-dba4f295610a): one field with `.start.page` fixed inside the field on the right; the publish button stays disabled until a name is typed; "Not Published" sits under the site name in the top bar and the button there reads "Publish Changes".
- [Base44 Edit URL](https://mobbin.com/screens/97da715f-f544-4dd2-94af-baeabde34e52): grey `https://` prefix, bold editable name, grey suffix, all in one input.
- [Mintlify](https://mobbin.com/screens/c71f4db8-2732-4072-b005-1c6c1d53508d): green row under the field, "Your site will be at **asmob**.mintlify.app"; a taken name gets "That site name is already taken. Please choose a different one."
- [PayPal username](https://mobbin.com/screens/e6f1f035-553f-4583-b838-05a47576c157): taken error with two clickable suggestions below it.
- [GoDaddy publishing](https://mobbin.com/screens/76eda10d-7978-458e-b73d-1d4d7a98ead1): modal swaps to a spinner with "Publishing. Just a moment…". [Webflow](https://mobbin.com/screens/8b7d9540-15cd-4752-9c8e-b9e3b14ed754) lists build steps, which is too technical for our owners.
- Success: [Base44](https://mobbin.com/screens/99190c15-95c7-4ff6-89d5-a1574742aee7) (address row with copy and open icons, share icons including WhatsApp), [Manus](https://mobbin.com/screens/2ff363b2-8333-47f6-afbd-b5aeed0b3371) ("Your site is now live!", Visit as the primary button), [GoDaddy](https://mobbin.com/screens/cc18a841-2f26-467d-9317-b88b1d91349e) (address shown in a mini browser bar), [Emergent](https://mobbin.com/screens/4940c74e-a958-40fe-aabc-13c1d7243004) ("This link is stable and will stay live unless you take it down").
- QR: [Delphi](https://mobbin.com/screens/2b05c0a4-18e1-4a35-843e-9dd32d3c6ab9) puts the QR at the top of the share modal; [Deputy](https://mobbin.com/screens/84a4ec1b-6465-45f4-a54f-08435673d1d8) offers "QR Code" and "QR Poster" as downloads.
- Changes after publish: [Gamma](https://mobbin.com/screens/6fb867d4-401e-4132-bee7-a0215bbf427b) ("Your site is up to date. Last published less than a minute ago" beside a Publish button), [Navattic](https://mobbin.com/screens/61242614-4b50-48b3-8b52-73b72b321629) (banner "This demo has unpublished changes" with Publish), [Ferndesk](https://mobbin.com/screens/00fd1378-211a-4249-aeee-bdcb2c19c6f1) ("Publish 1 change. This will update your public help center immediately").
- Unpublish: [GitBook](https://mobbin.com/screens/d67a2f82-2d4f-4386-b5d6-672e4c906a43) ("Your site will no longer be available to external audiences. You can republish your site with the same URL at any time", red button), [Base44](https://mobbin.com/screens/59bdb587-5a19-4301-a322-61f781b5bbad).

**Pattern to copy.** One modal, three views (choose address, publishing, live). Suffix fixed inside the field. Availability is checked as the owner types (400ms debounce) and answered in one line under the field: green with the full address, or red with a reason and two suggestions. No build log; a spinner and one sentence. Success leads with the address, then Copy link, Share on WhatsApp, Visit. QR code: yes, as a small "Download QR code" link, because owners print it for the counter (inferred use; the pattern is Deputy's). After the first publish the top-bar button becomes "Publish changes" and skips the address step.

```
FIRST PUBLISH                                   LIVE
┌ Publish your website ───────────── × ┐        ┌ Your website is live ────────────── × ┐
│ Choose your website address          │        │ ┌ ◦◦◦ sharmadental.cnote.site  [⧉] ┐ │
│ ┌──────────────────────┬───────────┐ │        │ └───────────────────────────────────┘ │
│ │ sharmadental         │.cnote.site│ │        │ [ Copy link ] [ Share on WhatsApp ]   │
│ └──────────────────────┴───────────┘ │        │ [        Visit website  ↗         ]   │
│ ✓ sharmadental.cnote.site is free    │        │ Download QR code · Change address     │
│ Small letters, numbers and hyphens.  │        │ ───────────────────────────────────── │
│ You can change this later.           │        │ Enquiries from your site will appear  │
│                                      │        │ in Leads.                [Open Leads] │
│            [Cancel] [Publish website]│        └───────────────────────────────────────┘
└──────────────────────────────────────┘
 taken:    ✕ That address is taken. Try: sharmadental-pune · sharmadentalclinic
 reserved: ✕ That address can't be used. Please pick another.
 invalid:  ✕ Use 3 to 40 small letters, numbers or hyphens.
 publishing: body replaced by spinner + "Publishing… this takes a few seconds."
             no close button, no Cancel

PUBLISH CHANGES (site already live): no modal. Button shows spinner, then toast
"Changes are live · Visit". Failure: danger toast "Couldn't publish. Your live site
is unchanged. [Retry]".
```

**Top bar.** Status pill replaces Phase 0's "Draft": `● Draft` (grey) → `● Live` (green) → `● Live · unpublished changes` (amber dot). The button is "Publish" (primary), then "Publish changes" (primary, only when there are changes), otherwise "Published ✓" (quiet, opens the Live view again). Undo of a change that is already live also sets the amber state (inferred).

**Unpublish confirmation** (from Settings only): title "Take your website offline?", body "Visitors to sharmadental.cnote.site will see a 'not available' page. Your design is kept and you can publish again any time with the same address.", buttons Cancel / "Take offline" (danger). No typing-to-confirm; it is reversible.

## 2. Site settings

**References**
- [Jobber SEO](https://mobbin.com/screens/e3e59115-c872-4da9-845e-94eca1763904): two fields, each with "Aim for 60 characters or fewer to avoid text being cut off", live preview beside them.
- [Later](https://mobbin.com/screens/9c762c20-5f21-4986-9410-dc67426953e2): defaults shown as placeholders, "70 characters left", and a preview labelled "search engines may show something different".
- [Squarespace](https://mobbin.com/screens/98f2f5dc-d2dd-4380-8da5-cbb5b7b0f04c) and [Lovable publish popover](https://mobbin.com/screens/b638db77-707b-491c-b3e0-11fc173a6dd9) (description with a 20/150 counter).
- [Jobber address](https://mobbin.com/screens/1c22d073-c862-40dc-b911-1a3d2d96e610) and [Gamma edit domain](https://mobbin.com/screens/6fb867d4-401e-4132-bee7-a0215bbf427b): changing the address warns "your old domain will stop working. Also, any links to the old domain will break."
- [Base44 settings](https://mobbin.com/screens/59bdb587-5a19-4301-a322-61f781b5bbad): "Platform Badge: show or hide the Base44 badge" row, then a "Danger Zone" with Unpublish.

**Pattern to copy.** A right-side sheet (480px, full screen on phone) opened from a gear in the top bar. Four groups in settings-row style. SEO fields are pre-filled from the business profile; the preview updates as the owner types. Plain labels: "Name shown on Google", not "title tag".

```
┌ Website settings ───────────────────────── × ┐
│ ADDRESS                                       │
│ sharmadental.cnote.site   ● Live     [Change] │
│ Changing it breaks links you already shared.  │
│                                               │
│ HOW YOU APPEAR ON GOOGLE                      │
│ Name shown on Google                   42/60  │
│ [Sharma Dental | Dentist in Kothrud, Pune ]   │
│ Short description                     118/155 │
│ [Family dental clinic in Kothrud. Root…   ]   │
│ ┌ Preview ──────────────────────────────────┐ │
│ │ sharmadental.cnote.site                   │ │
│ │ Sharma Dental | Dentist in Kothrud, Pune  │ │
│ │ Family dental clinic in Kothrud. Root …   │ │
│ └ Google may show this differently. ────────┘ │
│                                               │
│ PLAN                                          │
│ Free plan · 14 of 30 AI edits left            │
│ "Made with C Note" badge        shown  (lock) │
│ Removing the badge will be part of a paid     │
│ plan. [Tell me when it's available]           │
│                                               │
│ TAKE OFFLINE                                  │
│ Hide your website from visitors. [Take offline]│
└───────────────────────────────────── [Save] ──┘
```
Counters turn amber past the limit but never block saving (Jobber says "aim for"). Save is disabled until something changes. Address change reuses the publish field and its errors.

## 3. Images

**References**
- In chat: [Claude](https://mobbin.com/screens/7a8f884e-7db7-4944-967b-e2e5abca64a8) and [ClickUp](https://mobbin.com/screens/1959fd0a-8ee6-4ced-ac51-4b5d2390e9e8) show the image as a thumbnail above the user's bubble; [Langdock](https://mobbin.com/screens/b66870ae-8d47-493d-b1b6-e6c01173ad96) shows a pending attachment as a chip with × inside the composer, next to a `+` button.
- Block level: [Dribbble page builder](https://mobbin.com/screens/f2dc9ae0-0f42-4dee-bd14-34bce90c3da6) is the closest match to our builder: the same four-icon block toolbar, and a panel with the thumbnail, Change / Remove, and an Alt Text field. [Squarespace](https://mobbin.com/screens/5d66cdf5-61d6-423b-b7a8-3f0ab425fde8): Replace opens a menu starting with "Upload file". [Klaviyo](https://mobbin.com/screens/a13bc089-5c59-418c-b7c7-778b00935de8): alt text helper "Brief description of your image". [Digg](https://mobbin.com/screens/cc4c8002-9134-42c9-a53c-78e280772ba8): alt text with a 12/140 counter.
- Empty states: [Adobe Express](https://mobbin.com/screens/85b695b9-28f8-4528-b1c9-d4eca1266450) (grey area, icon, "Add your image"), [Savee](https://mobbin.com/screens/d13dd933-09ed-476a-b971-b6cd1a1ff7fe) ("Nothing here yet! Click to upload."), [Square](https://mobbin.com/screens/a3919f0c-a620-4a40-b9d1-3aff457372cb) ("Drag and drop images here, or [Upload]").
- Errors: [PayPal](https://mobbin.com/screens/51b7bb9e-116b-4004-984f-cb38b6f00d90) (red border on the drop zone, allowed types and size under it, one error line), [Whop](https://mobbin.com/screens/03fc821c-eec1-4278-bf97-8f8ac202d34c) (per-file error rows).

**Pattern to copy**
- Composer gets a `+` (image) button left of the text. A chosen image shows as a 48px thumbnail chip with × and a progress ring; Send waits for the upload. Dragging a file anywhere over the chat column shows a dashed overlay "Drop image to add it".
- With the image attached, suggestion chips become "Use as our logo", "Put it in the top section", "Add to gallery" (inferred). The sent message shows the thumbnail above the bubble; the change card reads "Logo added to Header · Undo".
- Image block toolbar adds one button before the Phase 0 four: `[Replace image]`. It opens the file picker directly; no media library in Phase 1.
- Empty image areas in the preview (builder only, never on the published site): `--site-primary` at 8% fill, dashed border, image icon, "Add a photo". Click = same as Replace. On the published site an empty image slot collapses and the block falls back to its text-only layout (inferred).
- Alt text: never a blocking prompt. After upload the assistant writes it and the change card shows "Described as: 'Front of Sharma Dental clinic' · Edit" (inferred; products seen all use a manual field). Edit opens a one-field popover, 140 characters, labelled "Describe this photo for people who can't see it".
- Limits line under any drop zone: "JPG, PNG or WebP, up to 10 MB" (inferred values).

## 4. Plan and usage

**References**
- [Webflow AI usage](https://mobbin.com/screens/834099db-be3d-4751-bfb4-005f9eea2a7d): "100% remaining", a bar, "200 / 200 credits remaining", and "Your monthly credits reset in 29 days".
- [Luma](https://mobbin.com/screens/4ad27330-2c65-4347-93cf-4a6926f4121d): a small pill at the bottom of the sidebar, "58% of credits remaining".
- [Emergent](https://mobbin.com/screens/8b787ef2-a28f-46ea-b95f-b512f3a6fbe5): the limit is an assistant message in the thread with one button, plus a dismissible strip above the composer when running low; the preview also carries a "Made with Emergent" pill bottom-right.
- [Gamma](https://mobbin.com/screens/cc5f5e1f-a525-4289-8dab-853800db87f7): balance modal with a "What uses credits?" accordion.
- Badge on a live page: [Zapier "Built on Zapier"](https://mobbin.com/screens/053f9a95-f2d0-4cbe-a89f-3f707dbf8276), small dark pill, bottom-right.

**Pattern to copy.** Say "edits", not credits; one unit = one applied AI change (failed or undone-before-apply changes don't count; Gamma states the same rule). The meter is a quiet line under the composer until 80% is used.

```
NORMAL (under composer, 12px muted)          LOW (5 or fewer left, amber strip above composer)
 14 of 30 edits left this month  ⓘ           ⚠ 3 edits left this month. Resets 1 Dec.      ×

ⓘ popover                                    LIMIT REACHED (assistant message + locked composer)
┌──────────────────────────────────┐         ┌─────────────────────────────────────────────┐
│ ▓▓▓▓▓▓▓▓░░░░░░░░  16 used of 30  │         │ You've used all 30 edits for November.      │
│ Resets on 1 Dec                  │         │ Your website stays live and enquiries keep  │
│ Each change I make uses 1 edit.  │         │ coming. New edits start on 1 Dec.           │
│ Moving, deleting and Undo are    │         │ More edits will come with a paid plan.      │
│ free.                            │         │ [Tell me when it's available]               │
└──────────────────────────────────┘         └─────────────────────────────────────────────┘
                                              composer: disabled, "Edits are back on 1 Dec"
                                              still free: toolbar move/duplicate/delete, Undo,
                                              Replace image, Publish, Settings
```
No price is shown anywhere. "Tell me when it's available" records interest and turns into "We'll let you know ✓" (inferred). **Badge:** published sites end with a small pill in the footer row, "Made with C Note", 12px, 60% opacity of `--site-text`, linking to the C Note site. In the footer flow rather than floating, so it never collides with the WhatsApp button (inferred).

## 5. Leads

**References**
- [QuickBooks Leads](https://mobbin.com/screens/1ef0e7ce-ac6e-4e72-bc54-58d91236f094): a "Sources" card lists "Contact forms" beside the lead table.
- [Pipedrive Leads Inbox](https://mobbin.com/screens/45d9181e-ad36-4146-91ea-93ce49aef464): unread dot per row and a "Source origin: Web Forms" column.
- [Wix form submissions](https://mobbin.com/screens/d4290a0f-470f-4ab7-9dcf-d463a02b8478): Submissions / Spam / Trash tabs and the empty state "No submissions yet. Once you have submissions, you'll see a detailed table of the responses here."
- Visitor side: [Eventbrite](https://mobbin.com/screens/04fcbdcf-2d96-4572-852f-c49f657a0064) (green tick, "Your message was submitted successfully! Organizers typically respond within 4 business days"), [Zapier](https://mobbin.com/screens/053f9a95-f2d0-4cbe-a89f-3f707dbf8276) ("Our Support hours are Monday to Friday with a 24-hour response time").
- Consent: [Savor](https://mobbin.com/sites/sections/ae1f5fe6-9420-45e4-b097-64ae2aa8c06e) (one grey line above the button: "By messaging us, you agree…"), [Wizz](https://mobbin.com/sites/sections/85b49850-cf07-4ae3-8c1f-7ab4e61f9551) (checkbox: "I consent to have this website store my submitted information so they can respond to my inquiry").

**Pattern to copy.** The builder does not grow its own inbox. Enquiries are C Note leads with source "Website".
- Top bar chip next to the status pill: `✉ 3 new enquiries` (hidden at zero). Click opens Leads filtered to source = Website.
- First time only, a card in the thread: "You got your first enquiry from your website: Priya, 'Do you do braces?' [Open in Leads]" (inferred).
- Before any enquiry, the Live view of the publish modal carries the one line shown in the wireframe above.
- Published form, success: the form is replaced in place by a tick, "Thanks, {name}. We've got your message.", "{Business} usually replies within a day." and a "Chat on WhatsApp" button. No redirect.
- Published form, failure: fields keep their values; a red row above the button says "Couldn't send. Please try again, or call {phone}." Field errors sit under the field, in words ("Enter a 10-digit mobile number").
- Consent: use the checkbox form, unticked, required: "I agree that {Business} can contact me about this enquiry." It is one more tap than Savor's passive line, but explicit consent is the safer default under India's DPDP Act (inferred; confirm wording with legal).

## 6. "Talk to a person"

**References**
- [Resend Help](https://mobbin.com/screens/27ace4ea-bb61-4141-b7aa-ae5551b66dfd): the AI chat on top and a separate, always-visible "Contact support" card under it.
- [Deel](https://mobbin.com/screens/db357932-0e83-43e7-89ff-5f47a06ef246): sets the expectation up front, "24/7 AI assistant… When needed, it will connect you with the right agent by email, chat, or call."
- [Klook](https://mobbin.com/screens/293a68c2-2a91-417b-96e0-c7b834a7d734): the bot offers a "Talk to someone" button inside its own message when it can't resolve the request.
- [Zapier](https://mobbin.com/screens/053f9a95-f2d0-4cbe-a89f-3f707dbf8276): hours and response time stated after the request is sent.

**Pattern to copy.** Two entry points. (a) A `?` in the chat header opens a small menu: "Talk to a person". (b) After two "Can't do" or Error turns in a row, the assistant adds the chip "Talk to a person" to its alternatives (Klook). Both open the same card in the thread:

```
┌ Talk to a person ──────────────────────────────┐
│ ● Open now · Mon–Sat, 10 am – 7 pm IST         │
│ [ Chat on WhatsApp ]   [ Request a call back ] │
│ We'll see your website and this chat, so you   │
│ don't have to explain again.                   │
└────────────────────────────────────────────────┘
 closed: ○ Closed now · opens tomorrow 10 am
         [ Leave a message ] → "Sent. We'll reply by 11 am tomorrow."
```
Hours and channels are placeholders (inferred). The chat stays usable; the handoff is a card, not a mode switch.

## 7. Published-site blocks

What makes the reference sections look professional, and what we copy: one accent colour used only for buttons and small labels; a small eyebrow label above each heading ([Fiasco "Our services"](https://mobbin.com/sites/sections/814e0a13-81d8-4d89-95f1-33b72ef0d9d3), [YLLW](https://mobbin.com/sites/sections/ce502cd9-c17f-45be-ad46-a91198465bdd)); generous, even vertical spacing; real photos with one consistent corner radius ([Front](https://mobbin.com/sites/sections/7f17ae2b-cd8c-4dbb-9594-4e73b63e3979)); short lines of text; and one clear action per section ([Mailchimp](https://mobbin.com/sites/sections/f2d58301-b6dd-4cce-9f8b-e106f0e764cf)).

**Shared rules (theme tokens)**
- Container: max 1120px; side padding 20px phone, 32px desktop. Section padding 56px phone, 96px desktop. Alternate sections use `--site-bg` and a tint (`--site-primary` at 6%) so blocks separate without lines.
- Type, all `--site-font`, colour `--site-text`: H1 32/38 phone, 56/60 desktop, weight 700. H2 24/30, 36/42. H3 18/26, 20/28, weight 600. Body 16/26, 18/28. Small 14/20. Eyebrow 12px, uppercase, 0.08em tracking, `--site-primary`. Muted text = `--site-text` at 70%.
- Buttons: 48px high, 20px side padding, radius `--site-radius`, weight 600. Primary = `--site-primary` fill with white or dark text chosen for 4.5:1 contrast. Secondary = 1px `--site-text` outline at 20%. Full width on phone inside Hero, CTA and ContactForm.
- Cards and images: radius `--site-radius`; card border 1px `--site-text` at 10%, no shadow. Tap targets at least 44px. Phone numbers are `tel:` links; addresses open maps.

| Block | Phone | Desktop | Reference and note |
|---|---|---|---|
| Header | 56px bar: logo or name left, `Call` icon button and menu button right; menu is a full-width sheet of section links. Sticky. | 72px: logo left, up to 5 anchor links centre-right, one primary pill button (Call / Book) far right. | [Ease](https://mobbin.com/sites/sections/6695790f-7142-4e99-86a9-2632963b795d), [sweetgreen](https://mobbin.com/sites/sections/ef00c77c-841c-4e12-b207-df44ce97a57a): few links, one pill. |
| Hero | Eyebrow (city or category), H1, one body line (max 2 sentences), primary + secondary button stacked, then image 4:3 below. | Two columns 6/6: text left, image right 4:3; or full-bleed photo with 45% dark overlay and centred text when a wide photo exists. | [Ease](https://mobbin.com/sites/sections/6695790f-7142-4e99-86a9-2632963b795d) (text then image), [Square](https://mobbin.com/sites/sections/29337e63-1f63-4b05-bdfd-672cd2a1f5ed) (photo, two buttons), [Beside](https://mobbin.com/sites/sections/bacfcd5d-bcca-4640-8fcf-da2f03dc8669) (phone number shown large). |
| Services | Eyebrow + H2, then one-column cards: 40px icon or 16:10 photo, H3, 2-line body, optional "from ₹…" line. 16px gap. | 3 columns (2 if only 2 or 4 items), 24px gap, equal height. | [Fibery](https://mobbin.com/sites/sections/8354be46-ea56-48e0-9057-617a0caff0db) (icon, title, text), [sweetgreen](https://mobbin.com/sites/sections/ef00c77c-841c-4e12-b207-df44ce97a57a) (photo cards). |
| About | Photo 4:3, eyebrow, H2, 1–2 short paragraphs, optional 3 facts row ("Since 2009 · 4 dentists · 12,000 patients"). | Two columns 5/7, photo left, text right, max 60 characters per line. | [YLLW](https://mobbin.com/sites/sections/ce502cd9-c17f-45be-ad46-a91198465bdd), [Dovetail](https://mobbin.com/sites/sections/18e6b8a2-4ebd-415b-aa97-46064c4dc296). Facts row is inferred. |
| Gallery | 2-column grid, square crops, 8px gap, optional caption 14px under each. Max 6 shown, "Show more" after. | 3 columns, 16px gap; first image may span 2 rows. Tap opens a simple lightbox. | [Front](https://mobbin.com/sites/sections/7f17ae2b-cd8c-4dbb-9594-4e73b63e3979), [Plain](https://mobbin.com/sites/sections/6849b42b-fd97-486b-b6a9-df81b07e7480). |
| Pricing | A price list, not plan cards: group heading H3, then rows "name … ₹price" with a dotted leader, optional 14px description under the name. | 2–3 group columns inside one card. | [Corgi](https://mobbin.com/sites/sections/9df7bc10-f0b2-4ad1-99b1-2dbab807ab6b), [Monte](https://mobbin.com/sites/sections/9cea9d4c-5557-44cf-97e4-41d7d086df12). Prices right-aligned, tabular numerals. |
| FAQ | H2, then accordion rows: 56px min height, question weight 600, `+` on the right, 1px dividers, first item open. | Same, in a 720px column; or heading left / list right at 4/8. | [DICE](https://mobbin.com/sites/sections/39efc6da-f1f5-4a4f-abd1-8f1009712c2e), [Origin](https://mobbin.com/sites/sections/1f1caf97-2f02-4668-bc84-05c7f760d6d2). Use `<details>` so it works without script. |
| CallToAction | Full-width band in `--site-primary`, white H2, one line, one white button (Call or WhatsApp). Centred, 48px padding. | Same band, text left and button right, or centred. | [Mailchimp](https://mobbin.com/sites/sections/f2d58301-b6dd-4cce-9f8b-e106f0e764cf), [Stripe](https://mobbin.com/sites/sections/708e5bdd-0c88-4278-ab8b-5f285ab062ea). |
| MapHours | Three stacked groups: Find us (address, `[Get directions]` button opening Google Maps), Opening hours (day–time table, today in bold, "Open now" chip), Contact (phone, WhatsApp). No embedded map. | Three columns, or address + hours left and a storefront photo right. | [Monte](https://mobbin.com/sites/sections/6ecba422-fe26-482b-8225-8e24ec959d84) (three columns and an "I need a real map →" link, no embed), [Little Amps](https://mobbin.com/sites/sections/74a3a0da-d93a-419b-a675-1a641b46a4eb) ("Go to map" button + photo), [Samara](https://mobbin.com/sites/sections/f53bcfb9-579b-43ad-8923-89e66973e8fe). "Open now" chip is inferred. |
| Testimonials | Horizontal snap scroll of cards at 85% width: quote mark, 3–4 line quote, name in small caps, optional area ("Kothrud"). No star ratings, no logos, since the owner types these in. | 2 or 3 column grid, equal height. | [Cursor](https://mobbin.com/sites/sections/310a71ab-a52e-466b-a6a0-7e225ee71967): quote, then name bottom-left. |
| ContactForm | H2 + one line, then fields stacked: Name, Mobile number (numeric keypad), Message (optional, 3 rows). Labels above fields, 48px inputs, consent checkbox, full-width button "Send enquiry". | Two columns 5/7: heading, phone and WhatsApp left; form in a card right. | [Savor](https://mobbin.com/sites/sections/ae1f5fe6-9420-45e4-b097-64ae2aa8c06e), [YLLW](https://mobbin.com/sites/sections/a45b58fa-4ac0-49f8-957e-47be93d4f4e1), [Wizz](https://mobbin.com/sites/sections/85b49850-cf07-4ae3-8c1f-7ab4e61f9551). Three fields only; email is not asked by default (inferred). |
| Footer | Dark band (`--site-text` as background, `--site-bg` as text): name, address, phone, hours summary, social icons, then © line and the "Made with C Note" pill. Extra 72px bottom padding so the WhatsApp button never covers it. | 3 columns (About / Visit / Contact) and a bottom row. | [Monte](https://mobbin.com/sites/sections/6ecba422-fe26-482b-8225-8e24ec959d84), [Touchy Coffee](https://mobbin.com/sites/sections/1262461a-ee8b-472e-9cf1-001b0de70548), [Grain](https://mobbin.com/sites/sections/4bcc55f8-e1a0-4f70-b86f-2b76449b191f). |
| WhatsApp button | Fixed bottom-right, 56px circle, 16px from the edges plus safe-area inset, WhatsApp green `#25D366` with the white glyph, soft shadow. Opens `wa.me` with a prefilled "Hi, I found you on your website". Hidden while the form's keyboard is open. | Same, 24px from the edges, with the label "Chat on WhatsApp" on hover. | Inferred. No small-business WhatsApp button was found on Mobbin; placement follows the floating chat bubbles on [Front](https://mobbin.com/sites/sections/7f17ae2b-cd8c-4dbb-9594-4e73b63e3979) and [Fibery](https://mobbin.com/sites/sections/8354be46-ea56-48e0-9057-617a0caff0db). |

**Five templates.** Palettes, fonts and orders are our proposal (inferred), drawn from the looks above (Ease's green and serif for care, Monte's terracotta on cream for food). All fonts are Google Fonts; Poppins, Mukta and Hind also cover Devanagari.

| Template | Vertical | `--site-primary` | `--site-bg` / `--site-text` | `--site-font` | `--site-radius` | Block order |
|---|---|---|---|---|---|---|
| Trusty | Services (plumber, CA, salon, tutor) | `#1D4ED8` | `#FFFFFF` / `#0F172A` | Inter | 10px | Header, Hero, Services, About, Testimonials, FAQ, CallToAction, ContactForm, MapHours, Footer |
| Bazaar | Shop (retail, boutique, sweets) | `#C2410C` | `#FFFBF5` / `#1C1917` | Poppins | 16px | Header, Hero, Gallery, Services (as "What we sell"), Pricing, Testimonials, MapHours, ContactForm, Footer |
| Care | Clinic (dentist, physio, diagnostics) | `#0F766E` | `#F8FAFA` / `#11302D` | Mukta | 12px | Header, Hero, Services (as "Treatments"), About (as "Meet the doctor"), MapHours, Testimonials, FAQ, ContactForm, Footer |
| Tadka | Restaurant (cafe, tiffin, caterer) | `#B4532A` | `#FAF3E7` / `#2B1A12` | Fraunces headings, Inter body | 4px | Header, Hero (full-bleed photo), Pricing (as "Menu"), Gallery, About, Testimonials, MapHours, CallToAction, Footer |
| Works | Manufacturer / trader (B2B) | `#F59E0B` | `#FFFFFF` / `#111827` | Hind | 2px | Header, Hero, Services (as "Products"), About (with facts row), Gallery, FAQ, ContactForm (button "Get a quote"), MapHours, Footer |

The WhatsApp button is on in all five. Tadka sets a second font, which needs a `--site-font-heading` token (new; falls back to `--site-font`).

## 8. Template picker

**References**
- [Kit](https://mobbin.com/screens/e68501f6-d531-44f4-95b7-17020742a3a2): each card has a thumbnail, a name, and two equal buttons, Preview and Choose; category filter beside the grid.
- [Squarespace](https://mobbin.com/screens/4b685ff1-ec9c-47fb-8479-8cd1a212b279): categories as one row of text tabs; card shows name and category.
- [Webflow](https://mobbin.com/screens/fd92ca38-c355-43ad-b15b-878a8d75e169): tall thumbnails showing the whole page, category under the name.

**Pattern to copy.** Screen A from Phase 0 with the chips row switched on: `[All] [Services] [Shop] [Clinic] [Restaurant] [Manufacturer]`. With one template per chip, a chip does not filter down to a lonely card; it reorders the grid so the match is first and carries a "Good fit" tag, and the chip matching the business profile is preselected (inferred). Cards keep Phase 0's 16:10 thumbnail, name, one-liner, `[Preview]` `[Use this]`. Thumbnails are rendered with the owner's business name and are top-aligned so the hero is what shows. Grid: 3 columns from lg, 2 at md, 1 on phone, where the chips scroll sideways. Switching template later is a chat request ("Change the design"), which reopens this picker in a modal and keeps the content (inferred).

## States

| State | Where | What the owner sees |
|---|---|---|
| Checking address | Publish modal | Small spinner at the right of the field; Publish disabled |
| Taken subdomain | Publish modal, Settings | Red line "That address is taken. Try: …" with two tappable suggestions; field keeps focus |
| Reserved / invalid | Publish modal, Settings | Red line with the rule in words; Publish disabled |
| Publishing | Publish modal or top-bar button | Spinner + "Publishing… this takes a few seconds."; modal cannot be closed; chat stays readable |
| Published | Modal Live view; top bar | Address row, Copy link, Share on WhatsApp, Visit, QR link; pill `● Live`; button "Published ✓" |
| Unpublished changes | Top bar | Pill `● Live · unpublished changes` (amber); primary button "Publish changes" |
| Publish failed | Toast or modal | "Couldn't publish. Your live site is unchanged." + Retry |
| Offline (unpublished) | Top bar, Settings | Pill `● Offline`; button "Publish"; address kept |
| Edits low | Above composer | Amber strip "3 edits left this month. Resets 1 Dec." (dismissible) |
| Limit reached | Chat | Assistant message with reset date and "Tell me when it's available"; composer disabled; toolbar, Undo, Publish still work |
| Uploading | Composer chip or image block | Thumbnail with progress ring; Send waits |
| Upload failed | Composer chip or image block | Chip turns red: "Couldn't upload. [Try again]"; too large or wrong type says which: "This photo is over 10 MB" / "Use JPG, PNG or WebP"; site untouched |
| Form sent | Published site | Form replaced by tick + "Thanks, {name}. We've got your message." + WhatsApp button |
| Form failed | Published site | Values kept; red row "Couldn't send. Please try again, or call {phone}." |
| New enquiries | Top bar | Chip `✉ 3 new enquiries` → Leads filtered to Website |
| Support closed | Help card | "Closed now · opens tomorrow 10 am" + Leave a message |
