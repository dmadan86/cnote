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
