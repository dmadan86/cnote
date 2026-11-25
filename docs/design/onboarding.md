# First-run onboarding

Status: design, 2026-11-25. Branch `feat/onboarding`. Nothing is built yet.

The owner asked: "For onboarding try to use wizard or any better ui ux, check with mobbin."

**Decision:** a five-step, fully skippable, full-page wizard right after signup, then a "Get set up" checklist on Home whose ticks come from real data. No product tour. Invited members get a one-screen welcome. Existing workspaces see nothing unless they open the guide from the user menu.

## 1. Today's flow, from the code

| Stage | What happens | Where |
|---|---|---|
| Signup form | Name, email, password, workspace name. One screen. | `apps/web/src/components/auth-form.tsx` |
| Server | Creates the user, then the workspace with the user as owner. | `apps/api/internal/server/authapi.go` `signup`, `auth/accounts.go` `SignUp`, `CreateWorkspace` |
| Pre-filled | One space "General" (`GEN`), two doc templates (Proposal, Invoice cover letter), seven ledger accounts (receivables, bank/UPI, CGST, SGST, IGST, sales, bad debt). | `CreateWorkspace` |
| First screen | `/w/{id}` Home. The heading says "Welcome back" even on the first visit. | `components/home/page.tsx` |
| First-run prompt | A modal, "Set up your company", for admins when `settings.business_type` is missing. Step 1: business type and size. Step 2: modules. Skip saves type `other`, so it never returns. | `components/business-type.tsx`, `shell.tsx` |
| Home | With `HOME_WIDGETS=1`: a widget grid, default layout by role, saved per person in `core.ui_configs` (screen `home`). Without the flag: the older dashboard. | `home/widgets-home.tsx`, `widgets/catalog.tsx`, `lib/screen-config.ts` |

**Modules** (`lib/modules.ts`): Home, Tables, Docs, Projects, CRM, Billing, Reports, Whiteboard, Website. `settings.modules` missing means all are on. Home and Reports are always on.

**Empty after signup, and needed before the product is useful:**

- **Company profile** (`company.get` / `company.update`): legal and trade name, PAN, GSTIN, state, address, pincode, phone, email, UPI, bank. The server validates the GSTIN, derives the state from it and rejects a PAN that contradicts it. There is no logo field.
- **Branches, departments, people**: empty apart from the owner.
- **Members**: Settings > Members calls `member.add`.
- **Data**: the import hub (`/w/{ws}/import`) has Contacts, Items, Opening balances and From Tally. The hub describes CSV files only. Finished jobs can be undone.
- **Website**: no site. **Tables**: no base; built-in templates exist (`table_template.list`).
- **Sample data**: none exists anywhere in the app.

### Problems found while tracing

1. **Choosing a business type hides Tables and Website.** Every preset in `lib/business-types.ts` lists only docs, projects, crm and billing, and step 2 of the modal offers only those plus Whiteboard. `moduleOn` then hides `tables` and `site`. I found no other module switch in Settings.
2. **An invited person appears to have no way to sign in with a password.** `member.add` creates a user row with no password and sends nothing: `member.added` has no subscriber. The auth routes are signup, login, logout, me and email verification. Signing up with that email returns "an account with this email already exists". Only SSO would work. This blocks the invite step (see BE-2).
3. **The modal is the whole onboarding.** It asks nothing about GSTIN, team or data, and it leaves Home with no next step.
4. **Every e2e test passes through it.** `e2e/helpers.ts` `signup` clicks Skip in the modal. Any change to first run must change that helper in the same merge.

## 2. Mobbin research

### Short personalisation wizard

- [Xero, completing onboarding setup](https://mobbin.com/flows/215632ce-e64c-4fc6-bff6-427026f68ce9?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): three questions, one per screen, three-segment progress bar. "What would you like to do in Xero?" is multi-select with a Recommended badge and the line "This helps personalise your Setup guide". Then "What do you want to do first?"
- [QuickBooks, completing onboarding setup](https://mobbin.com/flows/1a0a53a6-d570-41f9-be28-c537ae0d5831?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): industry is optional, role and headcount share a screen, then "Pick one and we'll do the rest after that".
- [Apollo](https://mobbin.com/screens/b4485ed8-5798-4405-be27-0d7edc8f2b59?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "Pick as many as you want. You'll still have access to everything."
- [Remote](https://mobbin.com/screens/ca8b4a41-7ac9-489f-ba16-a0155511c6fc?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): the first card picked is marked "top interest".

Anti-pattern: the full [QuickBooks signup](https://mobbin.com/flows/b9808087-6da2-4b52-8732-96f3eed897a4?tab=screens&utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) runs to 26 screens with plan choice and an SMS code before any value.

### Company basics

- [Mixpanel](https://mobbin.com/screens/2f2b3cae-db62-47b0-a40d-7f68af01999b?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): name, size and industry in one short form.
- [Attio](https://mobbin.com/screens/e8a73d4a-ff15-414a-92ce-a20bee5652d8?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a live preview of the workspace beside the form.
- [bunq, iOS](https://mobbin.com/screens/b312ab38-2f7d-4b55-93f9-9e94d45c3633?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "VAT Number (optional)" with a format placeholder.
- [Jobber, iOS](https://mobbin.com/screens/0ec0aca4-dae7-40f1-91e2-1e1232cca403?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): two fields and one full-width button.

### Invite the team

- [HubSpot](https://mobbin.com/screens/98aa9bf9-c279-475e-bc30-ce26c0e08813?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): the last step, "Skip, for now", and "About 3 minutes" in the rail.
- [Loops](https://mobbin.com/screens/b6c486fd-6a1a-4d2e-a2fb-ed4e06fc573e?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): one field, "Skip and send invites later".
- [Pin](https://mobbin.com/screens/bb1211ff-4c57-4f9b-aefd-f4983835cc54?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a role per invitee, "Continue without inviting".
- [Docusign](https://mobbin.com/screens/f3572dc6-160b-44ab-a9c3-80b31341f174?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): Skip is a full-size button under Invite.

### Setup checklist on Home

- Xero (last screen of the flow above): "Setup guide, 0 of 5 completed", a Hide button, and a "Preview with example data" switch.
- [HoneyBook](https://mobbin.com/screens/7c915d6b-2a99-4eb0-956d-e7a3a97bb265?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): each task shows a time estimate ("2 mins").
- [Shopify](https://mobbin.com/screens/01119441-a83f-4141-bc57-26eb0174ff5e?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "Add your first product ... Start with a sample product"; the card later reads [Products added](https://mobbin.com/screens/2483d325-15ba-4b24-a732-8b468f614960?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) from real data.
- [Outseta](https://mobbin.com/screens/b1bcf27d-e787-4a4d-81f7-a1ec5515f5fe?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): the guide can be removed when finished.

### Sample data and empty states

- [Zoho CRM](https://mobbin.com/screens/36f3e04a-dcf0-498a-82bb-4979a2ef5d88?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): a "Load Sample data" tick in the getting-started form.
- [Workable](https://mobbin.com/screens/4335e19b-f15e-4d9d-a8ff-f26b3aef28e0?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): "Your account is now populated with sample data."
- [Attio](https://mobbin.com/screens/fae1e15f-0c02-40de-ba70-16d2677cc71b?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): an empty list offers Import CSV and Connect data source.
- [Braintrust](https://mobbin.com/screens/5e2e07a5-4fb6-49be-8403-a1a4dc564843?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): two equal buttons, upload or start empty.

### Tours versus contextual tips

- [Zoho CRM tour](https://mobbin.com/screens/4e90996d-cb99-4e9e-8c70-a87e61c0f65b?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): seven sections of tooltips over a dimmed screen.
- [Ditto](https://mobbin.com/screens/5c487a97-d9ac-4153-969b-c5cb5c584255?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): step 3 of 9.
- [Notion](https://mobbin.com/screens/fa7a1586-e22b-433e-aaca-333b90f495ac?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp): no tour, a welcome page with tick boxes.

### Gaps in the research

Mobbin returned nothing for Vyapar, Khatabook, myBillBook or Zoho Books; those queries returned unrelated apps. I did not search Slack, Airtable, ClickUp, Asana, Pipedrive, Freshworks, Stripe or Razorpay individually.

### What this means for a busy, non-technical owner

- **Steps:** three to five, one question per screen. Xero and HubSpot stay inside that; QuickBooks does not.
- **Skip rules:** every step has a visible Skip of normal size. Say "you can change this later".
- **Time to first value:** land in the product with a checklist rather than finish every setting first. Xero, Shopify and HoneyBook all do this.
- **Mobile:** a single column, large cards, one button pinned to the bottom, optional fields labelled "(optional)".
- **Tours:** skip them. A nine-step overlay is the first thing an owner closes.

## 3. The design

### 3.1 Flow

```
signup ──> /welcome/{ws}  step 1..5 (each skippable, "Skip setup" always visible)
                │
                └──> /w/{ws} Home, personalised, with "Get set up" checklist
                     (or the import page, if they chose to import)
```

The wizard is a full page outside the shell, so there is no sidebar to distract and it works on a phone. It replaces the `BusinessTypeChooser` modal.

Each step saves when the person presses Continue, so closing the tab loses nothing. Back keeps answers. Skip moves on and saves nothing for that step. "Skip setup" marks onboarding done and goes Home.

### 3.2 Step 1: your business

Reuses `BusinessTypePicker` and `CompanySizePicker`.

```
┌──────────────────────────────────────────────────────────┐
│ C Note                                     Skip setup    │
│ Step 1 of 5  ━━━━ ──── ──── ──── ────     About 2 minutes│
│                                                          │
│ What does your business do?                              │
│ This sets up C Note for you. You can change it later.    │
│                                                          │
│ ┌──────────────────────┐ ┌──────────────────────┐        │
│ │ ( ) Service company  │ │ ( ) Project company  │        │
│ │ Installation, repair │ │ Contractors, EPC     │        │
│ └──────────────────────┘ └──────────────────────┘        │
│ ┌──────────────────────┐ ┌──────────────────────┐        │
│ │ ( ) Trading company  │ │ ( ) Manufacturing    │        │
│ └──────────────────────┘ └──────────────────────┘        │
│ ┌──────────────────────┐ ┌──────────────────────┐        │
│ │ ( ) Recurring services││ ( ) Professional firm│        │
│ └──────────────────────┘ └──────────────────────┘        │
│ ┌──────────────────────┐                                 │
│ │ ( ) Something else   │                                 │
│ └──────────────────────┘                                 │
│ How many people?  (1-10) (11-50) (51-200) (200+)         │
│                                                          │
│                                  [ Skip ]  [ Continue ]  │
└──────────────────────────────────────────────────────────┘
```

### 3.3 Step 2: what you want to do first

Multi-select. The first card picked is the "first goal" and gets a "Start here" tag. The type from step 1 pre-ticks its usual goals.

```
│ Step 2 of 5  ━━━━ ━━━━ ──── ──── ────                    │
│                                                          │
│ What do you want to do first?                            │
│ Pick as many as you like. Everything else stays          │
│ available in Settings.                                   │
│                                                          │
│ ┌────────────────────────────┐ ┌────────────────────────┐│
│ │ [x] Send GST invoices      │ │ [x] Track leads and    ││
│ │     and get paid           │ │     customers          ││
│ │     Start here             │ │                        ││
│ └────────────────────────────┘ └────────────────────────┘│
│ ┌────────────────────────────┐ ┌────────────────────────┐│
│ │ [ ] Run projects and tasks │ │ [ ] Keep stock         ││
│ └────────────────────────────┘ └────────────────────────┘│
│ ┌────────────────────────────┐ ┌────────────────────────┐│
│ │ [ ] Staff and attendance   │ │ [ ] Build a website    ││
│ └────────────────────────────┘ └────────────────────────┘│
│ ┌────────────────────────────┐ ┌────────────────────────┐│
│ │ [ ] Notes and documents    │ │ [ ] My own tables      ││
│ └────────────────────────────┘ └────────────────────────┘│
│                                                          │
│                        [ Back ]  [ Skip ]  [ Continue ]  │
```

| Goal key | Turns on | Moves up on Home |
|---|---|---|
| `invoices` | billing | `kpi.sales`, `chart.sales`, payments tile |
| `crm` | crm | `kpi.deals`, `chart.pipeline`, inquiries tile |
| `projects` | projects | `list.tasks`, tasks tile |
| `stock` | billing | low stock tile |
| `staff` | (not gated by `lib/modules.ts` today) | `check_in`, people tile |
| `website` | site | `kpi.site` |
| `docs` | docs | none |
| `tables` | tables | none |

`settings.modules` becomes the union of the goals' modules. Skipping the step leaves `settings.modules` unset, so everything stays on. This fixes problem 1. `homeDefaults` gains one optional argument, the first goal, and moves that goal's widgets directly under the checklist. People who have already arranged their Home are not touched.

### 3.4 Step 3: company basics

```
│ Step 3 of 5  ━━━━ ━━━━ ━━━━ ──── ────                    │
│                                                          │
│ Your business details                                    │
│ These print on your quotations and invoices.             │
│                                                          │
│ Business name                                            │
│ [ Sharma Electricals                          ]          │
│ State                                                    │
│ [ Maharashtra                               v ]          │
│ GSTIN (optional)                                         │
│ [ 27ABCDE1234F1Z5                             ]          │
│ 15 characters, as on your GST certificate.               │
│ Leave empty if you are not registered for GST.           │
│                                                          │
│                        [ Back ]  [ Skip ]  [ Continue ]  │
```

- Business name is pre-filled with the workspace name from signup and saved as `legal_name`.
- GSTIN is uppercased as typed. The client checks the shape on blur with `^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`. The server (`company.update`) stays the authority.
- A valid GSTIN sets State from its first two digits and locks the State field with the note "Set from your GSTIN". The server does the same.
- Errors, shown under the field: "This GSTIN is not 15 characters." "This GSTIN does not look right. Check it against your GST certificate."
- Address, PAN, bank and UPI are left to the checklist.

### 3.5 Step 4: invite your team

```
│ Step 4 of 5  ━━━━ ━━━━ ━━━━ ━━━━ ────                    │
│                                                          │
│ Invite your team                                         │
│ They get an email with a link to join. You can add       │
│ more people later.                                       │
│                                                          │
│ Email                             Role                   │
│ [ accounts@sharma.in           ]  [ Member v ]           │
│ [                              ]  [ Member v ]           │
│ + Add another                                            │
│                                                          │
│                  [ Back ]  [ Skip for now ]  [ Send ]    │
```

Three rows at most in the wizard; more in Settings > Members. Roles: Member (default) and Admin. One `member.add` call per row; a failed row keeps its text and shows its error, the others still go.

**This step ships only with BE-2.** Without an invite email and a way to set a password, the step is left out and the wizard has four steps.

### 3.6 Step 5: starting data

```
│ Step 5 of 5  ━━━━ ━━━━ ━━━━ ━━━━ ━━━━                    │
│                                                          │
│ How do you want to start?                                │
│                                                          │
│ ┌──────────────────────────────────────────────────────┐ │
│ │ ( ) Bring my data from Tally                         │ │
│ │     Parties, items and balances from TallyPrime.     │ │
│ ├──────────────────────────────────────────────────────┤ │
│ │ ( ) Upload a CSV file                                │ │
│ │     Customers, vendors or items. In Excel, use       │ │
│ │     Save as > CSV.                                   │ │
│ ├──────────────────────────────────────────────────────┤ │
│ │ ( ) Try it with sample data                          │ │
│ │     10 customers and 10 items. Remove them any time. │ │
│ ├──────────────────────────────────────────────────────┤ │
│ │ ( ) Start empty                                      │ │
│ └──────────────────────────────────────────────────────┘ │
│                                                          │
│                              [ Back ]  [ Finish setup ]  │
```

- **Tally** and **CSV** finish the wizard and open the existing `/w/{ws}/import/tally` or `/w/{ws}/import`. The importer is not rebuilt inside the wizard.
- **Sample data** sends two bundled CSV files (contacts, items) through the existing data import, so they show up in the import hub with the existing Undo. No sample invoices: an invoice takes a number from the real series and posts to the ledger.
- **Start empty** goes Home.

### 3.7 Landing: Home with the checklist

The heading reads "Welcome, {first name}" until the checklist is done or hidden, then "Welcome back".

```
┌─ Get set up ─────────────────────────── 2 of 6 done ── ✕ ┐
│ ━━━━━━━━━━━━──────────────────────────                   │
│ ✓ Tell us about your business                            │
│ ✓ Add your business name and state                       │
│ ○ Add your address and GSTIN          2 min        >     │
│ ○ Add your first customer             1 min        >     │
│ ○ Send your first invoice             3 min        >     │
│ ○ Invite a team member                1 min        >     │
│                                                          │
│ Using sample data. Remove sample data                    │
└──────────────────────────────────────────────────────────┘
```

It is a widget, type `setup`, size `l`, in `homeWidgets`, first in `homeDefaults` for owners and admins.

Every tick is computed by the server in `home.summary`; nothing is ticked by hand.

| Item | Done when | Shown when | Opens |
|---|---|---|---|
| Tell us about your business | `settings.business_type` is set | always | `/welcome/{ws}` |
| Add your business name and state | profile has `legal_name` and `state_code` | always | Settings > Company |
| Add your address and GSTIN | profile has `address`; GSTIN is not required | billing on | Settings > Company |
| Add how customers pay you | profile has `upi_vpa` or `bank_account` | billing on | Settings > Company |
| Add your first customer | at least one contact that is not sample data | crm or billing on | CRM, new contact |
| Send your first invoice | at least one invoice that is not a draft | billing on | Billing, new invoice |
| Invite a team member | more than one active member | BE-2 shipped | Settings > Members |
| Publish your website | a site is published | site on | Website |
| Create your first table | at least one base | tables on, goal `tables` | Tables |

At most six rows show: done ones first, then open ones in table order.

**Hide:** the ✕ asks nothing and hides the widget for that person. **Bring back:** user menu > "Setup guide". The shell has no help menu today, so the item goes in the user menu, above Settings. **Finished:** the widget shows "You're set up. Hide this" once, then hides on the next visit.

When `HOME_WIDGETS` is off, the same component renders as a plain card above the old dashboard.

### 3.8 First-run empty states

All use the existing `EmptyState` (icon, title, one line, one main button, one quiet link).

| Screen | Title | Line | Main button | Quiet link |
|---|---|---|---|---|
| Invoices | No invoices yet | Make a GST invoice in under a minute and send it by WhatsApp or email. | New invoice | Import from Tally |
| Quotations | No quotations yet | Send a quotation. When the customer agrees, turn it into an invoice in one click. | New quotation | |
| Contacts | No customers yet | Add the people you sell to and buy from. | Add customer | Import a CSV file |
| Deals | No deals yet | Track each inquiry from first call to payment. | Add deal | |
| Projects | No projects yet | Break a job into tasks, each with an owner and a due date. | New project | |
| Stock | No items yet | Add what you sell, with its HSN code and GST rate. | Add item | Import a CSV file |
| Attendance | Nobody has checked in yet | Your team checks in from their phone. Add them first. | Add people | |
| Website | No website yet | Describe your business and get a first draft to edit. | Create website | |
| Tables | No tables yet | Keep any list your way: orders, assets, vendors. | Start from a template | Start blank |
| Docs | No pages yet | Write SOPs, scopes of work and meeting notes next to the work. | New page | |
| AI employees | No AI employees yet | Give routine work to an assistant. It asks before it sends or changes anything. | Add AI employee | |

If the owner lacks the GSTIN and opens "New invoice", the form shows one line above it: "Your invoices will not show a GSTIN yet. Add it in Settings > Company."

### 3.9 Re-entry

| Who | What they see |
|---|---|
| New owner, wizard not finished | The shell sends them back to `/welcome/{ws}` at the step they reached. |
| New owner, wizard finished or skipped | Home with the checklist. |
| Invited member, first visit | No wizard. One dialog: "Welcome to {workspace}. {Owner} added you as {role}. You can use: {modules}." Button: "Start". Their Home is the role default. |
| Existing workspace | Nothing. No wizard, no welcome, no checklist. The checklist appears only if they choose "Setup guide". The old modal is removed, so admins who never picked a type stop being asked on every load. |

The discriminator is one key: `settings.onboarding`. New workspaces get it at creation; existing ones never have it.

### 3.10 Where state lives

No migration for the wizard or checklist.

**Workspace settings** (`workspace.update`, existing jsonb merge). The merge is shallow, so `onboarding` is always written whole.

```
settings.business_type   existing
settings.company_size    existing
settings.modules         existing
settings.onboarding = {
  v: 1,
  step: 3,                      // furthest step reached, for resume
  goals: ["invoices", "crm"],   // first entry is the first goal
  start: "tally" | "csv" | "sample" | "empty",
  skipped: ["invite"],          // steps skipped
  done_at: "2026-11-25T10:12:00Z"   // absent while in progress
}
```

**Per person** (`ui_config`, screen `onboarding`):

```
{ v: 1, checklist: "open" | "hidden", welcomed: true }
```

**Migration:** only BE-2 needs one, for invite tokens. `1325` is the latest on main, so `1326` is free today. `feat/tables-1b` is active; take the next free number at merge time.

### 3.11 Accessibility

- One `h1` per step. On step change, focus moves to it and the title announces "Step 2 of 5".
- The progress bar is decoration; the text "Step 2 of 5" carries the meaning.
- Type and data choices are native radios in a `fieldset` (the existing card pattern); goals are native checkboxes. Arrow keys and Space work without extra code.
- Enter submits the step. Skip, Back and "Skip setup" are real buttons in tab order after the fields.
- Each error is tied to its field with `aria-describedby` and announced with `role="alert"`. Colour is never the only signal.
- No auto-advance and no time limit.
- Checklist: a list. Each row is one link whose name includes its state ("Done: Add your first customer"). The ✕ is labelled "Hide setup guide".
- Tap targets are at least 44px. Motion respects `prefers-reduced-motion`.

### 3.12 Mobile

- One column. Cards are full width; steps 1 and 2 scroll.
- A bar pinned to the bottom holds Back, Skip and Continue, above the on-screen keyboard.
- GSTIN field: `autoCapitalize="characters"`, `autoCorrect="off"`. Email fields: `type="email"`.
- The checklist is the first thing on Home and spans the full width.
- The native app in `apps/mobile` is not changed.

### 3.13 Hindi and regional readiness

The web app has no translation layer. This design does not add one; it keeps the copy ready for one.

- All wizard, checklist and welcome text lives in one file, `lib/onboarding-copy.ts`, as a flat map of key to string.
- No sentence is built by joining pieces. Values go in named placeholders: `"{done} of {total} done"`.
- Plain words, no idioms. "GSTIN", "HSN", "UPI" and "Tally" stay as they are in every language.
- Layouts allow text about 40% longer and wrap rather than cut off.
- Numbers, the GSTIN and emails stay in Latin digits.

### 3.14 Measuring drop-off

The web app has no analytics library. The funnel can be read from `settings.onboarding` with SQL: `step`, `skipped`, `start` and `done_at` give the step reached, what was skipped and whether setup finished. No new store.

If a tracker is added later, these are the event names:

| Event | Properties |
|---|---|
| `onboarding_started` | |
| `onboarding_step_done` | `step`, `skipped` |
| `onboarding_finished` | `start`, `goals`, `seconds` |
| `onboarding_abandoned` | `step` (skip setup pressed) |
| `checklist_item_opened` | `item` |
| `checklist_hidden` | `done`, `total` |
| `checklist_completed` | `days_since_signup` |

### 3.15 End-to-end tests

- `helpers.ts` `signup`: lands on `/welcome`, presses "Skip setup" by default. This must merge with the web change or every spec fails.
- `onboarding.spec.ts`:
  1. Full path: Trading, goals invoices and website, valid GSTIN, skip invite, start empty. Expect the nav to show Billing and Website, the state to be set from the GSTIN, and the checklist to show.
  2. Bad GSTIN shows the error and blocks Continue; Skip still works.
  3. Skip setup: all modules on, checklist shows, the wizard does not return.
  4. Resume: reload at step 3 returns to step 3.
  5. Checklist ticks itself after a contact is created; ✕ hides it; "Setup guide" brings it back.
  6. Invited member: no wizard, welcome dialog once.
  7. Workspace without `settings.onboarding`: no wizard, no checklist.
  8. Keyboard only, at 390px wide.
- `business-type.spec.ts`: rewritten against the wizard; the Settings half stays.
- Go tests: `home.summary` setup block per role and module; `CreateWorkspace` sets the key.

## 4. Work packages

Files do not overlap between packages. BE-1 and the web packages can run together against the contract below.

**Contract.** `home.summary` gains, for owners and admins only:

```
setup: { business: bool, profile: bool, address: bool, payment: bool,
         contact: bool, invoice: bool, member: bool, site: bool, table: bool,
         sample: bool }
```

| Package | Owns | Work |
|---|---|---|
| **BE-1** backend | `apps/api/internal/auth/accounts.go`, `modules/home/home.go`, their tests | Set `settings.onboarding = {v:1, step:1}` in `CreateWorkspace`. Add the `setup` block from existence checks on current tables; no counters. |
| **BE-2** backend, invites | `modules/admin/admin.go` (`memberAdd`), new `auth/invite.go`, `server/authapi.go`, mailer template, one migration | Email the invited person a single-use link to set a password. Needed for step 4. |
| **WEB-A** wizard | new `app/welcome/[ws]/page.tsx`, new `components/onboarding/wizard.tsx`, new `lib/onboarding.ts` and `lib/onboarding-copy.ts`, `lib/business-types.ts`, `components/business-type.tsx`, `components/auth-form.tsx`, `components/shell.tsx` | The five steps. Remove `BusinessTypeChooser`. Redirect and resume. "Setup guide" menu item. Member welcome dialog. Add `tables` and `site` to the module list. |
| **WEB-B** checklist and empty states | new `components/widgets/setup.tsx`, `widgets/catalog.tsx`, `home/page.tsx`, `home/dashboard.tsx`, the module pages in the empty-state table | The widget, `homeDefaults` first-goal ordering, the greeting, the empty-state copy. Reads state through `lib/onboarding.ts`. |
| **TEST-DATA** | `e2e/helpers.ts`, `e2e/onboarding.spec.ts`, `e2e/business-type.spec.ts`, `e2e/fixtures/`, `apps/web/public/samples/*.csv` | Tests in 3.15. The two sample CSV files with plausible Indian names, valid GSTINs and HSN codes. |

Order: BE-1 and TEST-DATA's sample files first; WEB-A and WEB-B in parallel; the helper change merges with WEB-A.

Left out on purpose: a product tour, a module settings screen, sample invoices, a translation framework, an analytics vendor, a logo upload, branches in the wizard, changes to the native app.

## 5. Open questions for the owner

1. **Invites.** Is there a sign-in path for invited people that I missed, or is it SSO only today? If BE-2 is not wanted now, the wizard ships with four steps and no invite step.
2. **Sample data.** Customers and items only, removed with the import Undo. Is that enough, or do you want sample invoices and deals too? Those need a separate, safer design. The Undo window and whether an import can start without its preview screen are unchecked.
3. **Excel.** The importers take CSV. Is "Save as CSV" acceptable copy, or is `.xlsx` upload required for launch?
4. **`HOME_WIDGETS` in production.** Is the widget Home on for customers? The checklist works either way, but the first-goal layout only applies to the widget Home.
5. **Staff and attendance, stock.** These are not switches in `lib/modules.ts`. Should the goals only reorder Home for them, as designed, or should they become modules that can be turned off?
6. **Phone number.** Indian SMB apps usually sign up by mobile number and OTP. This design keeps email and password. Is that right for now?
7. **Language.** Is Hindi wanted for launch, or is copy readiness enough?
8. **Existing workspaces.** They get nothing by default. Do you want a one-time, dismissible "New: setup guide" notice for them?

## 6. Binding revisions (owner decisions and design review, 2026-11-25)

This section overrides sections 3–5 wherever they disagree. Builders read it first.

### 6.1 Owner decisions

1. **Lean first release.** A 3-step skippable wizard, plus a "Get set up" guide on Home that links to existing flows.
2. **No sample data** in this release. Tables templates keep their own sample rows.
3. **Business type sets only labels and Home order.** It never hides a module.
4. **Invites become a proper email-invite feature** (6.6), built and reviewed on its own. The wizard has no invite step. The guide links to it once it ships.

Cut from sections 3–5: step 4 (invite team), step 5 (starting data and sample CSVs), goal-driven module switching, the member welcome dialog, the `sample` flag, and the SQL funnel. The empty-state copy in 3.8 ships only where 6.5 says so.

### 6.2 Entry: never blocks the app (review 4, 11)

- **No global redirect and no gate in the shell.** Only the signup form routes to the wizard: after a successful signup, `auth-form.tsx` goes to `/w/{ws}/welcome` instead of `/w/{ws}`. The wizard lives inside the workspace layout, so auth, membership and expired sessions follow the existing shell rules.
- **Every step has "Skip for now"** and the header has "Go to C Note". Both navigate to Home even if a save fails, and a failed save shows a retry toast on Home. A failed read shows the step empty, never an error page.
- **Re-entry.** `/w/{ws}/welcome` stays reachable from the guide ("Finish setup") for owners and admins. Members who open it are sent to Home. Additional workspaces created later go to the same wizard from the create-workspace flow, if that flow routes after creation; otherwise they go to Home.
- **The old "Set up your company" modal is removed**, and its trigger condition is deleted. Its business-type picker moves into step 1.

### 6.3 Steps

1. **Your business.** Business type (the existing presets) and business name. It saves through the existing business-type save, changed per 6.4.
2. **Company basics.** State (required to continue, skippable), GSTIN (optional, format and state-code check on the client, server validation as today), address (optional). It saves through `company.update`.
3. **What do you want to do first?** One choice: send an invoice, add customers, build a website, make a table, or import from Tally/Excel/CSV. It saves the choice as a Home ordering hint, then opens that flow directly: the invoice composer, the new-contact dialog, the website builder, Tables new base, or the import hub.

Completion is stored with `onboarding.complete`, a new server command (owner and admin). It sets `settings.onboarding.done_at` only if it is unset, inside one `UPDATE … SET settings = jsonb_set(…) WHERE …`, so it is monotonic and never overwritten. Step data lives in the real records (company profile, business type), so there is no per-step progress object to race on (review 6). The wizard uses no migration.

### 6.4 Business type (review 5)

- `applyBusinessType` stops writing `modules`.
- Nothing in the web app hides a module because of the stored `modules` list. Today only presets write it, and there is no Settings screen for it. Existing workspaces get every module back with no data change; the list is read only as Home ordering.
- Test: pick each business type, then confirm that Tables, Website and Whiteboard are present in the navigation.

### 6.5 "Get set up" guide (review 9, 10, 13)

- **Command.** A separate command `setup.status` (owner and admin only, workspace-wide, not branch-scoped) returns `[{key, done, href}]`. If it fails, only the guide shows "Couldn't load"; `home.summary` is untouched.
- **Rows, each an `EXISTS` on its own module's tables:**
  - company profile: name and state set
  - first customer
  - first invoice
  - website published
  - first table
  - data imported
  - team invited: shown only once 6.6 ships; it counts accepted, active members other than the owner

  Rows for a module the workspace lacks are left out.
- **Display.** Open items first, at most 5 rows, then "Show all (N)". The header reads "3 of 6 done". When every eligible row is done, the guide shows "All set" with "Remove from Home".
- **One source of truth for visibility: the Home widget layout.** The guide is a catalogue widget. It is in the default owner and admin layouts and nowhere else. "Hide" removes it from the saved layout through the existing widget edit path. "Setup guide" in the user menu re-adds it at the top without touching the rest of a customised layout. There is no separate `onboarding` ui_config screen.
- **Empty states.** In this release, the 3.8 copy is applied only on the customers list, invoices list, Tables home and website builder start. Each page's real empty predicate and the user's permission are checked first, and an action is hidden when the user cannot perform it (review 14).

### 6.6 Email invites (separate feature, design before build; review 1, 2, 3)

- `core.invitations` (migration **1327 or later**, checked again at integration): workspace, email (normalised), role, invited_by, token_hash (SHA-256 of 32 random bytes from crypto/rand), expires_at (7 days), accepted_at, revoked_at.
- **Creating an invite** (`member.invite`, admin) never creates a membership or a user. The role rules match `member.add` today: only owners may invite owners, and admins may invite at most admin.
- **Rate limits.** Issuing is limited per inviter and per workspace. Resending rotates the token. Revoking is possible at any time.
- **Accept link** `/invite/{token}`. The token is consumed atomically (`UPDATE … WHERE token_hash=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now() RETURNING …`) in the same transaction that creates the membership.
  - **Existing account:** the person must be signed in as that email, then accepts. An invite never sets or changes the password, MFA or sessions of an existing account.
  - **New email:** the person sets a password and the account is created verified, because they proved control of the inbox.
  - **Already a member, or deactivated:** a clear message, and nothing changes.
- **Responses are generic.** "If this email can join, an invite has been sent" — no account enumeration. Tokens are never logged, the page sends `Referrer-Policy: no-referrer`, and analytics never see the URL.
- **Today's `member.add`** (create a user with no password) stays for SSO workspaces only, and its no-password users can claim access through an invite.
- **Tests:** existing password user, SSO user, pending, expired, revoked, replayed, concurrent accept, already member, deactivated, role escalation, and enumeration responses.

### 6.7 Tests and work packages (review 15)

- **e2e/helpers.ts** `signup`: after signup it clicks "Skip for now" on the wizard. Business-type tests use the wizard's step 1 through an explicit option, replacing `skipPicker: false`.
- **New e2e:**
  - full wizard run, landing in the chosen flow
  - skip at each step
  - a failed save still reaches Home
  - a member opening /welcome lands on Home
  - the guide's rows tick from real actions
  - hide and restore without losing a customised layout
  - business type keeps every module
- **Packages:**
  - **O1 (backend):** `onboarding.complete`, `setup.status`, the `applyBusinessType` change.
  - **O2 (web):** the wizard, the auth-form route, modal removal, and the helper change.
  - **O3 (web):** the guide widget, the user-menu entry, and the four empty states.
  - **Invites:** a separate design review, then its own build after O1–O3 merge.
- **Integration.** The lead owns `e2e/helpers.ts` and the merge order: the helper, the route and the modal removal land in one commit.
