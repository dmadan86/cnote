# UI research (Mobbin, 11 Nov 2026)

Patterns chosen for C Note's web client, with the Mobbin screens they come from.

## App shell: icon rail + space sidebar + breadcrumb bar
- Narrow left icon rail for modules (Home, Projects, Docs, CRM, Billing, Whiteboard, Settings), a
  second sidebar with workspace switcher, "New item" button, and the Space tree; top bar with breadcrumb,
  centered global search (⌘K), layout toggles and a primary action at right.
  [Plane work items](https://mobbin.com/screens/61dfbba7-52d5-4a45-88a8-f3c5a8d516d2)
- Grouped secondary nav ("Records", "Lists") with counts. [Attio deals](https://mobbin.com/screens/c9b96d38-5a3a-42a0-826d-d1399639f468)

## Items list grouped by status (Projects default view)
- Rows grouped under status headers with counts and an inline "+ New work item" per group;
  key (ENG-12) at left, title, then status pill, priority pill, assignee avatar at right.
  [Plane](https://mobbin.com/screens/61dfbba7-52d5-4a45-88a8-f3c5a8d516d2)
- Query bar with a Basic / query-language toggle and a natural-language input above it; "Save as" view.
  This is exactly CNQL. [Plane PQL](https://mobbin.com/screens/17ebd9cd-d91f-4d73-8a4f-b312b03c294c)

## Boards and pipelines
- Columns with a colored dot, name and count, "+ New" at the top of each column, compact cards
  (title, owner, amount), and a per-column sum footer ("Add calculation").
  [Attio pipeline](https://mobbin.com/screens/c9b96d38-5a3a-42a0-826d-d1399639f468),
  [Square projects board](https://mobbin.com/screens/8d75c04b-1d38-4fd8-8340-de79ce82ef47)

## Living doc page (ADR-013)
- Centered reading column, minimal top bar with page name, a status badge and "Updated today", and a
  single Edit/Verify action. [hilos](https://mobbin.com/screens/ff4b5d10-6e61-44bc-a0a7-3319cbd3e394)
- Right-hand panel for version history / changes. [Plane page history](https://mobbin.com/screens/afc55a5a-4236-4f12-859d-9d38a55bd06d)
- Page actions menu with "Set type" (Decision, Meeting note, Project): our Types on a Note.
  [Tana](https://mobbin.com/screens/447c4c61-8356-43aa-9a08-e14b958e2ea9)
- C Note addition: freshness pill (Verified until 12 Feb / Needs review / Unverified / Archived),
  owner avatar, one-click "Verify" and an "AI suggested edit" diff card.

## Record detail (Item, Deal, Contact)
- Two columns: main content and line items left, property cards (customer, notes, addresses) right,
  timeline with comment box at the bottom. [Shopify draft order](https://mobbin.com/screens/068a5a10-0221-4070-9a4a-e66edb66aae7)

## Invoice editor (ADR-008)
- Line items table (item, qty, price, total) with drag handles and delete, "Add an item" row, totals
  card (subtotal, tax, total), and a right "settings at a glance" panel.
  [Square new invoice](https://mobbin.com/screens/c70df137-fb09-4dda-8c8b-9da3cba51deb)
- "Draft saved at ..." indicator and a primary "Review invoice" before issuing.
  [Stripe edit invoice](https://mobbin.com/screens/6280181a-1070-4c44-8793-a832c78bd7c5)
- C Note addition: GST panel (supplier GSTIN, place of supply, CGST/SGST vs IGST split per line,
  HSN/SAC), IRN status with the 30-day window countdown, immutable once issued (credit note action).

## Visual language
Neutral light UI (white canvas, #f7f7f8 sidebars), 13-14px UI text, 1px hairline borders, small
radius (6px), pills for status/priority, one accent color for primary actions, dark mode via tokens.

## Mobile and tablet app (apps/mobile)
- Home as a grouped list: one rounded badge icon per module, title and hint, chevron; workspace and account
  sections below. [GitHub iOS home](https://mobbin.com/screens/2210aab7-8323-40e4-98a6-1739eec50ac3)
- Floating, rounded tab bar that hovers above the content with safe-area inset; on iPad it is centred at a fixed
  width instead of stretching.
- Pages list grouped by day (Today, Yesterday, dates) with a floating "New page" pill bottom right.
  [Notes list with floating create](https://mobbin.com/screens/e563596d-d08a-4b32-a085-711680e3b381)
- Projects: filter chips (space, status category) over a flat item list, key and priority as subtitle, status
  pill at right. [GitHub Issues](https://mobbin.com/screens/ac207bf0-458c-4a91-86df-6772d5f421e8)
- Tablet (width >= 768dp): list on the left, detail on the right, no push navigation. Same components as the
  phone detail screens. Colours reuse the web tokens (light and dark follow the system).

## Page sharing and restrictions
- One dialog, three bands: add-people input with a permission select and primary button at the top, "People
  with access" list (avatar, name and email, role select at right), "General access" row with a lock icon and a
  plain-English select. [Luma share board](https://mobbin.com/screens/d703a534-b693-49e3-918b-3b85004019c1)
- Access level as a sentence under the select ("Only users with direct access can view this File"), and a
  separate "Copy share link" action. [Gumloop share file](https://mobbin.com/screens/9edc163f-8ec1-4e77-833d-eaff9207348c)
- Footer pairing of the access select (left) and Copy link (right); a tinted notice explains side effects.
  [Dropbox Dash share](https://mobbin.com/screens/d151525e-3dc4-44c1-afb5-32970ac22777),
  [Recraft share](https://mobbin.com/screens/1e3771c8-4ccb-4f5e-b84d-e2bb788050d1)
- C Note: restriction select with three plain options (anyone in the space / anyone views, listed people edit /
  only listed people), "Inherited from <page>" notice, lock icon beside restricted pages in the sidebar tree,
  "Shared with me" sidebar section, and a read-only editor for view-only people.

## Leads and follow-ups (WP3)

References (Mobbin): contact timeline with day-grouped activity cards and a details rail
(Calendly, https://mobbin.com/screens/8158ffb0-f197-4691-8149-271f7a1ab997), task/phone-call engagement card
(Unify, https://mobbin.com/screens/8c875cb9-3095-4047-afce-c0e89038dfa9), work-item activity feed with a log-work
popover (Plane, https://mobbin.com/screens/e7d1109a-c28d-4c7c-8e6c-3329fcdd6a2b), short contact form with optional
fields (Base44, https://mobbin.com/screens/5aceb3d1-f931-4b3e-93d1-1cfe441a56a7).
Codex consult applied: Inbox rows are phone cards with call and WhatsApp links and "Log outcome" (an unanswered lead
leaves the Inbox only when something is logged); follow-ups default to Today, a "No date" tab keeps unplanned deals
visible, snooze shows the new date with Undo; the summary is optional and chips show real dates; Lost asks for a reason
only after Lost is chosen and Cancel changes nothing; 44px targets and 16px inputs; honeypot out of tab order.

## Customer share links, accept and pay (WP2)
- Hosted invoice page: amount due as the hero, one primary pay action, details below; the owner side shows the
  hosted page URL with copy and download beside it. [Stripe invoice detail](https://mobbin.com/screens/aa09d42b-c83f-4a4f-8cf1-8e8152f7bdef),
  [Stripe review and send](https://mobbin.com/screens/5bfaf363-136c-4027-9cf3-fc2eea1aede6)
- Client view with a "View online" QR on the printed document. [Square client view](https://mobbin.com/screens/ff3c2be2-17af-4d0d-981f-ba2b18731e34)
- Accept / edit / decline with the agreed amount restated before the primary action. [Upwork proposal](https://mobbin.com/screens/5131f77b-ffff-48aa-a608-d252676b712c)
- Share dialog with the link, a notice about who can open it, and an unpublish action. [Plane publish](https://mobbin.com/screens/f6e00ee6-635c-41d4-b762-1876fc7ec11f),
  [Square share project link](https://mobbin.com/screens/b8c5b53c-ddfc-43ca-aacc-f14c00142daf)
- Codex critique applied: acceptance is a two-step review with a required name (never on open or Enter); the UPI
  QR is labelled "Scan to pay by UPI" and the GST e-invoice QR "not for payment"; payment never shows "Paid"
  from the page; line items stack on phones; actions and forms are hidden in print; expired, revoked and unknown
  links share one neutral "Link unavailable" state; the new link is shown once and older links list as
  status, views and revoke; 44px touch targets and 16px inputs on the public page.
