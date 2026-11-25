# Docs parity research: Confluence Cloud feature catalogue and C Note inventory (25 Nov 2026)

Companion to `docs/design/docs-parity.md` (gap matrix and build plan). This file is the evidence: what Confluence Cloud does today, with sources, and what C Note's docs product does today, with file references.

## How to read the sources

- **F** = the page was fetched and the claim read from it.
- **S** = the claim comes from a search-result summary; the page itself was not opened. Treat as likely but unconfirmed.
- **UNVERIFIED** = nothing solid was found. Do not rely on it.
- `SUP/x` abbreviates `https://support.atlassian.com/confluence-cloud/docs/x`.

Limits of this research, stated once:

- Atlassian's pricing page, G2's review pages (HTTP 403) and Reddit could not be read. Prices and all user-sentiment points are **S** at best.
- The Confluence sections of the 2026 weekly release notes were too large to read in full, so 2026 editor, whiteboard and database changes are thin.
- `atlassian.com/roadmap/cloud` was not read. Nothing in this file describes unreleased roadmap items.
- Three conflicts between official pages were found and are flagged where they occur: the page co-editing limit (29/30 vs an older 12), which plans include Automation, and the user ceiling for paid plans (250,000 vs 50,000).

## Part 1. Confluence Cloud feature catalogue

### 1.0 What changed in 2025-2026

| Change | Detail | Source |
|---|---|---|
| Space roles replace the permission grid | Four default roles (Admin, Manager, Collaborator, Viewer) plus up to 10 custom roles; general rollout from July 2026; new sites are role-only | F `SUP/faq-role-based-access-in-confluence/`, F `SUP/what-are-confluences-roles/`; dates S https://community.atlassian.com/forums/Confluence-Cloud-Admins-articles/Coming-soon-Role-based-access-control-in-Confluence-GA/ba-p/3248592 |
| Live docs are a first-class content type | No drafts, no publish step; convertible to and from pages | F `SUP/create-and-collaborate-in-real-time-with-live-docs/` |
| Macros renamed | Page properties is now "content properties"; Page tree is "content tree"; Children display is "Child items"; Include page is "Include content"; page owner is "content owner" | F `SUP/insert-the-page-properties-report-macro/`, F `SUP/insert-the-page-tree-macro/`, F `SUP/use-macros-to-show-confluence-content-on-pages/` |
| Synced blocks | A block reused across pages, live docs and Jira work items, updating in near real time | F `SUP/create-and-use-synced-blocks/` |
| Atlassian Intelligence is now Rovo | Support URLs still say "atlassian-intelligence"; official rename date UNVERIFIED | F `SUP/atlassian-intelligence-features-in-confluence-cloud/` |
| Rovo credits | Per user per month: Standard 25, Premium 70, Enterprise 150; pooled; overage $0.01 per credit; pay-as-you-go billing from 3 Dec 2026 | F https://support.atlassian.com/rovo/docs/rovo-usage-limits/ |
| Automation billed by "steps" | Billing starts 3 Dec 2026 | S https://support.atlassian.com/cloud-automation/docs/how-is-my-usage-calculated/ |
| Page width | Narrow / Wide / Max, with a site default | S https://community.atlassian.com/forums/Confluence-articles/New-feature-Compact-Mode-and-Max-Width-to-support-information/ba-p/3190468 |
| XML space export ends | End of life 1 Dec 2026; CSV replaces it | F https://support.atlassian.com/confluence/kb/xml-export-end-of-life-confluence-cloud/ |
| Legacy macros removed | 46 macros unavailable or limited; Section and Column macros deprecated from Jan 2026 (S) | F `SUP/learn-which-macros-are-being-removed/` |
| Redesigned version history, Rovo multi-site search, Rovo-written automation rules | Weekly release notes Jan-Sep 2026 | S https://confluence.atlassian.com/cloud/blog/2026/09/atlassian-cloud-changes-sep-21-to-sep-28-2026 |
| List price rise of roughly 7-10% | Effective 13 Oct 2026 | S https://www.adaptavist.com/blog/atlassian-price-updates-effective-october-2026 |
| Earlier milestones | Guests GA Aug 2023; page ownership Jul 2023; public links Oct 2023; whiteboards GA Apr 2024; Verified status Apr 2024; folders and databases Sep 2024 | F https://www.atlassian.com/software/confluence/features/whats-new |

### 1.1 Spaces

| Feature | Behaviour | Plan | Source |
|---|---|---|---|
| Space purpose at creation | Collaboration (live docs, whiteboards, databases; project plan, decision, meeting notes templates), Knowledge base (edit privately then publish; how-to and troubleshooting templates), or Custom | all | F `SUP/create-a-space/` |
| Space key and owner | Key A-Z 0-9, editable by admins; one owner (user or group) | all | F `SUP/create-a-space/` |
| Personal spaces | UNVERIFIED on current pages | | |
| Space overview (home) | Auto-created page; another page can be chosen; header image; built from macros (recent updates, excerpt include, live search, blog posts, content report) | all | F `SUP/set-up-your-space-homepage/` |
| Sidebar | Toggle overview, blog, apps; admin-ordered shortcuts to pages, spaces or URLs; folders toggle per space | all | F `SUP/organize-your-space/`, F `SUP/use-folders-to-organize-your-work/` |
| Archive a space | Supported | all | S https://support.atlassian.com/confluence/kb/how-to-archive-a-confluence-space/ |
| Space permissions / roles | Free: everyone can view and edit, not customisable. Paid: Admin, Manager, Collaborator, Viewer plus custom roles built from granular permissions (manage access, manage all content, create templates, look and feel, public links, guest access, anonymous access, delete, archive, export) | Standard+ | F `SUP/learn-about-confluence-cloud-plans/`, F `SUP/what-are-space-permissions/` |
| Guests | External users limited to one space, default Viewer; up to 5 guests per paid user on Standard | Standard+ (S) | S `SUP/invite-guests-for-external-collaboration/` |
| Anonymous access | Site switch plus per-space grant; off by default | Standard+ | S `SUP/set-up-public-access/` |
| Space templates | Space admins create templates under Space settings; global templates by site admins | all | F `SUP/create-a-template/` |
| Look and feel | PDF export styling per space (F); themes and colours UNVERIFIED | | F `SUP/customize-exports-to-pdf/` |

### 1.2 Page tree and lifecycle

| Feature | Behaviour | Plan | Source |
|---|---|---|---|
| Content types in the tree | Pages, live docs, folders, whiteboards, databases, smart links; blog posts listed by date | all | F `SUP/organize-your-space/`, F whats-new |
| Move and reorder | Drag and drop; moving a parent moves its subtree with attachments and comments | all | F `SUP/create-edit-and-publish-a-page/`; S `SUP/move-copy-and-hide-pages/` |
| Copy | Duplicate to a chosen space and parent, optionally with children | all | S `SUP/move-copy-and-hide-pages/` |
| Delete, trash, restore, purge | Deleted items sit in the space trash until purged; children of a deleted parent move up | all | S `SUP/delete-restore-or-purge-a-page/` |
| Archive | Removed from tree, quick search and analytics; read-only; restorable. Bulk archive up to 500 per run | archive Standard+; bulk Premium+ | F `SUP/archive-pages/` |
| Drafts and publish | Pages have private drafts with autosave; Publish sets location, access, version comment and whether to notify watchers; scheduled publish; edits to a published page are "unpublished changes" with a diff | all | F `SUP/create-edit-and-publish-a-page/` |
| Unpublish back to draft | UNVERIFIED | | |
| Live docs | No drafts or publish; 40 editors and 100 viewers; version finalised 10-15 minutes after the last edit | not gated (F) | F `SUP/create-and-collaborate-in-real-time-with-live-docs/` |
| Folders | Containers without a body; deleting a folder moves contents up | all | F `SUP/use-folders-to-organize-your-work/` |
| Content status | Rough draft, In progress, Ready for review suggested; up to 5 custom per space, 20 characters, 5 colours; separate **Verified** status | tier not stated | F `SUP/add-a-status-to-your-page-or-blog/`, F whats-new |
| Content owner | One owner, transferable, shown in the header | all | F `SUP/get-to-know-the-new-and-simplified-content-layout/` |
| Review date / expiry | **No native review-by date or expiry**; done with Automation or Marketplace apps | | S https://community.atlassian.com/forums/discussion/3140789/page-owner-and-review-date-on-conflunce-page |
| Page width | Narrow, Wide, Max | all | S (see 1.0) |
| Cover image and title emoji | Header image from Unsplash or upload, repositionable; emoji shown in the tree | all | F `SUP/make-your-page-and-its-title-more-memorable/` |
| Content manager | Bulk archive, delete and status change; inactive filter defaults to 180 days | Premium+ | F `SUP/archive-pages/`; S https://community.atlassian.com/forums/Confluence-Cloud-Admins-articles/Manage-your-stale-pages-in-bulk/ba-p/2592287 |

### 1.3 Editor

**Core elements** (all F `SUP/insert-elements-into-a-page` unless noted)

| Element | Behaviour |
|---|---|
| Panels | `/panel`: info, note, success, warning, error presets; custom panel with 20 background colours and optional emoji |
| Expand | `/expand`: collapsible section |
| Status lozenge | `/status`: coloured text label |
| Date | `//` opens a picker |
| Mention | `@name` notifies; people, teams and AI agents can be mentioned (F `SUP/collaborate-on-content-and-get-notified-in-confluence-cloud/`) |
| Emoji | `:` or `/emoji`, with site custom emoji |
| Code snippet | Language auto-detect (CSS, Go, HTML, Java, JavaScript, JSON, Python, Ruby, Rust, Shell, SQL, TypeScript), wrap, line numbers, format button |
| Action item | `/action` or `[]`; assign by @mention; due date turns red when overdue; appears in the assignee's task list |
| Decision | `/decision` or `<>`; a Decision report macro lists them |
| Divider, quote, headings H1-H6 | Standard |
| Text formatting | Bold, italic, underline, strike, code, sub/superscript, alignment, text and highlight colour, indent (F `SUP/format-text/`) |
| Markdown and autocomplete | `**`, `*`, `~~`, backticks, `#`, lists, `>`, `---`; triggers `:` `@` `!` `{` `/` (F `SUP/keyboard-shortcuts-markdown-and-autocomplete/`) |
| Find and replace | In More actions (F `SUP/create-edit-and-publish-a-page/`) |
| Paste from Google Docs or Word keeping formatting | UNVERIFIED |

**Tables and layouts**

| Feature | Behaviour | Source |
|---|---|---|
| Tables | Size menu; insert, drag-reorder, multi-select rows and columns; merge and split; header row and header column; numbered column; sort; cell colour; sticky header (not with merged header rows); column resize and distribute; fixed widths; table width and alignment; "Insert chart" from a table | F `SUP/simplify-data-with-tables/` |
| Layouts | 2 to 5 columns, sidebar variants, drag a block beside another to create a column | F `SUP/create-and-manage-layouts/` |

**Content macros**

| Macro | Behaviour | Source |
|---|---|---|
| Table of contents | From headings; level range, numbering, horizontal or vertical | F `SUP/insert-the-table-of-contents-macro/` |
| Child items | Links to nested items | F `SUP/use-macros-to-show-confluence-content-on-pages/` |
| Content tree (+ search) | Tree from a root with sort, depth, excerpts, search box | F `SUP/insert-the-page-tree-macro/` |
| Excerpt / Insert excerpt | Mark a section and show it on another page | S `SUP/insert-the-excerpt-include-macro/` |
| Include content | Embed a whole page elsewhere; about 10+ includes slow a page | S `SUP/insert-the-include-page-macro/` |
| Synced blocks | `/sync`; destinations view-only, permission-aware | F `SUP/create-and-use-synced-blocks/` |
| Content by label | List labelled pages; up to 500 items | S `SUP/insert-the-filter-by-label-macro/` |
| Content properties and report | Key/value table on a page; report tabulates pages with a label, 30 per page, filterable | F `SUP/insert-the-page-properties-macro/`, F `SUP/insert-the-page-properties-report-macro/` |
| Anchor | Link to `#anchor`; headings have link icons | F `SUP/insert-links-and-anchors/` |
| Attachments | Lists files attached to the page | F `SUP/what-are-macros/` |
| Others | Recent updates, blog posts, change history, contributors, labels list, popular labels, live search, user list, profile, task report, decision report, create-from-template button | F `SUP/what-are-macros/` |

**Media, links, embeds**

| Feature | Behaviour | Source |
|---|---|---|
| Images | Upload, resize (handles or pixels), caption, alt text, border, alignment and wrap, link | F `SUP/work-with-images-videos-and-files/` |
| Video and audio | MP4, MOV, AVI, MP3, WAV upload; YouTube and Vimeo by URL | F same |
| Files | Inline, thumbnail or card with full-screen preview; exact previewable types UNVERIFIED | F `SUP/display-files-and-images/` |
| Smart links | URL, inline, card or embed views; default per domain | F `SUP/insert-links-and-anchors/` |
| Figma, Google Docs, YouTube | Through smart-link embeds | F `SUP/learn-which-macros-are-being-removed/` |
| Loom | Record from the page action bar; embed detail UNVERIFIED | F `SUP/get-to-know-the-new-and-simplified-content-layout/` |
| iFrame | Generic embed macro | F `SUP/insert-the-iframe-macro/` |
| Jira | Issues macro (JQL, list or count), Jira chart, create work items from selected text or a table | F `SUP/insert-the-jira-issues-macro/` |
| Charts | Only from a table ("Insert chart"); the standalone Chart macro can no longer be inserted | F `SUP/learn-which-macros-are-being-removed/` |
| Roadmap planner | Lanes, bars, markers; not in live docs | F `SUP/insert-the-roadmap-planner-macro/` |
| Diagrams (draw.io, Gliffy) | Marketplace apps, not native | UNVERIFIED as "no native"; no native macro found |
| Math / LaTeX | Marketplace only | S https://marketplace.atlassian.com/apps/1228577 |

### 1.4 Templates

| Feature | Behaviour | Source |
|---|---|---|
| Gallery | Atlassian-provided templates and blueprints; total count UNVERIFIED. Long-standing blueprints: meeting notes, decision, how-to, troubleshooting, product requirements, retrospective, task report, file list | F whats-new; S https://confluence.atlassian.com/doc/blueprints-323982376.html (Server-era list) |
| Space and global templates | Space admins and site admins respectively | F `SUP/create-a-template/` |
| Variables | `/variable`: text, multi-line text, list. No date type listed. Filled in by hand when the page is created | F same |
| Placeholder text | `/placeholder`, visible only while editing | F same |
| Labels on templates | Applied to pages made from them | F same |
| Limits | No images or attachments in templates | F same |
| Create-from-template button | A macro placed on a page | S `SUP/insert-the-create-from-template-macro/` |

### 1.5 Collaboration

| Feature | Behaviour | Plan | Source |
|---|---|---|---|
| Co-editing | Pages up to 29 other editors (an older figure of 12 exists: conflict); live docs 40 editors | all | F `SUP/create-edit-and-publish-a-page/` |
| Inline and page comments | On pages, live docs, whiteboards, blogs; resolve/reopen detail UNVERIFIED on fetched pages | all | F `SUP/collaborate-on-content-and-get-notified-in-confluence-cloud/` |
| Reactions and likes | A "react to content" topic exists; set UNVERIFIED | | F same |
| Notifications | Bell for shares, mentions, comments and likes on watched content; items drop after 30 days; watching a space does not create bell items; email subscription exists, digest settings UNVERIFIED | all | F `SUP/view-your-notifications/` |
| Watching | Pages, live docs, spaces, whiteboards, blogs | all | F collaborate page |
| Tasks | Assigned by @mention with due date; personal task list; Task report macro filtered by space, page, assignee, label, date | all | S `SUP/insert-the-task-report-macro/` |
| Page analytics | Byline with views and read time on Standard+; full per-viewer list on Premium+ | see left | F `SUP/view-insights-on-pages/` |
| Space and site analytics | Usage and views | Premium+ | F `SUP/learn-about-confluence-cloud-plans/` |
| Home | Recent, starred, activity feed | all | S `SUP/use-home-to-jump-into-work-and-see-whats-happening/` |
| Team calendars | | Premium+ | S `SUP/use-team-calendars-for-confluence/` |
| Approvals | A native "request and manage approvals" page exists; detail UNVERIFIED | | `SUP/request-and-manage-approvals-in-confluence/` |
| Presenter mode, blogs | Exist; detail UNVERIFIED | | F shortcuts page |

### 1.6 History

| Feature | Behaviour | Source |
|---|---|---|
| Version list | Date, author, version comment | F `SUP/create-edit-and-publish-a-page/` |
| Compare | Select versions; unchanged blocks collapse (S for the collapse detail) | F/S same |
| Restore | Copies the old version as the newest | F same |
| Delete a version | Permanent, renumbers later versions | F same |
| Version comment and notify watchers | Chosen at publish | F same |
| Whiteboards and databases | 30-day history | S `SUP/whiteboard-history/`, S `SUP/database-version-history/` |

### 1.7 Permissions and sharing

| Feature | Behaviour | Plan | Source |
|---|---|---|---|
| Page restrictions | View restrictions inherit down the tree; edit restrictions do not | paid | S `SUP/add-or-remove-page-restrictions/` |
| Public links | View-only link to the latest version; controlled at site, space and page level; audit-logged | Standard+ | S `SUP/set-up-and-manage-public-links/` |
| Request access | UNVERIFIED | | |
| Inspect permissions, admin key | Diagnose one user's access; temporary audited bypass | Premium+ | S `SUP/premium-admin-tools-for-confluence-cloud/` |

### 1.8 Search

| Feature | Behaviour | Source |
|---|---|---|
| CQL | Fields include ancestor, contributor, created, creator, label, lastModified, mention, parent, space, text, title, type, watcher, favourite; operators `=`, `!=`, comparisons, `IN`, `~` | F (CQL field reference; exact URL UNVERIFIED) |
| Search filter UI | Specific filter chips UNVERIFIED | |
| Recent, starred, drafts | Sidebar lists; Recent has "Worked on" and "Owned by me" tabs | S `SUP/improved-confluence-navigation/` |
| Quick find | Recent items, results, spaces, people | S `SUP/search-for-pages-and-posts/` |
| Labels | On content and attachments; label pages and macros | `SUP/use-labels-to-organize-your-content/` (S) |
| Rovo search | Natural-language answers limited to what the user can see | F https://support.atlassian.com/rovo/docs/rovo-usage-limits/ |

### 1.9 Import and export

| Feature | Behaviour | Source |
|---|---|---|
| Single-page export | Word or PDF, published version only, portrait | F `SUP/export-content-to-word-pdf-html-and-xml/` |
| Space export | PDF, CSV, HTML, XML (XML ends 1 Dec 2026) | F same |
| PDF styling | Per space with CSS: title page, header, footer, page size | F `SUP/customize-exports-to-pdf/` |
| Import Word / Google Docs / OneDrive | `.docx` only; 5 MB single, 30 files and 50 MB in bulk | F `SUP/import-content-into-confluence-cloud/` |
| Import Notion | Zip of a workspace or page tree | F `SUP/import-data-from-notion-into-confluence/` |
| Markdown import | Not supported directly | S `SUP/insert-confluence-wiki-markup/` |

### 1.10 Integrations

| Feature | Behaviour | Source |
|---|---|---|
| Jira | Macros, smart links, create work items from text | S `SUP/insert-the-jira-issues-macro/` |
| Microsoft Teams | App can create pages from a channel tab | S `SUP/use-microsoft-teams-and-confluence-together/` |
| Slack | A Confluence Slack app was not confirmed | UNVERIFIED |
| Loom | Record and embed | S https://support.atlassian.com/loom/docs/use-loom-with-jira-and-confluence/ |
| Marketplace | Leading apps: draw.io, Gliffy (diagrams); Table Filter, Charts and Spreadsheets; Scroll PDF and Word Exporter; Comala Document Management (approval workflows); page-review reminder apps. Install counts are S only | S https://marketplace.atlassian.com/addons/app/confluence/top-selling |
| REST API v2 | Points-based rate limits since 2 Mar 2026 | S https://developer.atlassian.com/cloud/confluence/rate-limiting/ |
| Webhooks | Recommended over polling; Cloud specifics UNVERIFIED | S same |

### 1.11 AI (Rovo)

| Feature | Behaviour | Source |
|---|---|---|
| Summarise | Page, comments, changes since last visit, whiteboard | F `SUP/atlassian-intelligence-features-in-confluence-cloud/` |
| Write and edit | Draft, change tone, fix grammar, translate | S `SUP/write-or-edit-content-using-atlassian-intelligence/` |
| Content reviewer | Flags outdated information, tone, grammar | F features page above |
| Definitions | Explains terms and acronyms in place | F same |
| Q&A | Natural-language answers over accessible content | F `SUP/use-atlassian-intelligence-to-search-for-answers/` |
| Create with Rovo | Pages, whiteboards, databases, slides from a prompt (uses credits) | F `SUP/create-new-content-items-with-rovo/` |
| Automation by prompt, agents, audio briefings | Exist; plan detail UNVERIFIED | F features page |
| Plans | Listed for Standard and above with credit allowances; Free UNVERIFIED | F rovo usage limits |

### 1.12 Whiteboards and databases

| Feature | Behaviour | Source |
|---|---|---|
| Whiteboard tools | Stickies, pen, text, lines and connectors, shapes, sections, stamps, images, links, templates, voting, timer, private mode, comments | F `SUP/getting-started-with-confluence-whiteboards/` |
| Whiteboard limits | Free 3 boards per user, Standard 10, Premium unlimited; smart sections and connectors Premium+; 20,000 elements (S) | F `SUP/understand-confluence-whiteboards-plans-and-features/` |
| Whiteboard to Jira | Turn stickies into work items | F `SUP/organize-and-explore-ideas-with-sticky-notes/` |
| Whiteboard imports | Miro, Mural, FigJam | S |
| Database layouts and fields | Table, card, board; text, number, date, tag, user, link, image, page link, Jira item, entry link and backlink | F `SUP/create-and-edit-databases/`, F `SUP/database-fields/` |
| Database views | Saved views with filters, sort, hidden fields | F `SUP/database-views/` |
| Embed in a page | `/embed database`: whole database, one entry or one value | S |

### 1.13 Automation

| Feature | Behaviour | Source |
|---|---|---|
| Rules | Trigger, condition, action, branch; task branches on assignee, due date, status | S https://support.atlassian.com/cloud-automation/docs/actions-in-confluence-automation/ |
| Plan | **Conflict**: the plan page says Premium+ only (F); the pricing snippet lists step allowances on every tier (S) | F `SUP/learn-about-confluence-cloud-plans/` |
| Common rules | Monthly stale-page reminder to owners with a "needs-review" label; bulk archive | S https://community.atlassian.com/forums/Confluence-Cloud-Admins-articles/Keeping-Confluence-Content-Fresh-with-Automation/ba-p/3024065 |

### 1.14 Mobile

| Feature | Behaviour | Source |
|---|---|---|
| View, notifications | Yes | F `SUP/view-your-notifications/` |
| Create and edit, tables, media | Per store listing | S https://apps.apple.com/us/app/confluence-cloud/id1006971684 |
| Offline | Edit only items opened while online; no new content; cleared after 30 days; conflicts resolved by hand. Mobile-specific limits not stated | F `SUP/edit-content-offline/` |

### 1.15 Admin, plans and scale

| Feature | Behaviour | Plan | Source |
|---|---|---|---|
| Audit log | 1 to 12 months retention, CSV export | Standard+ | S `SUP/view-the-audit-log/` |
| Data residency | 11 regions including **India** | Standard+ | S https://support.atlassian.com/security-and-access-policies/docs/understand-data-residency/ |
| Classification levels | Admin-defined labels on pages | UNVERIFIED tier | S `SUP/classify-a-page-or-blogpost/` |
| Retention and trash purge rules | UNVERIFIED | | |
| IP allowlist, sandbox, release tracks | | Premium+ | F plans page |
| Storage | Free 2 GB, Standard 250 GB, Premium unlimited | | F plans page |
| Users | Free up to 10 | | F plans page |
| Attachment size | 100 MB default, configurable | | S `SUP/configure-attachment-size` |
| Price | Standard about $5.8 and Premium about $11.2 per user per month at 300 users (aggregators only) | | UNVERIFIED against Atlassian |
| Large tables | Editing tables of 100+ rows is slow; Atlassian tracked it as CONFCLOUD-81975 | | S https://jira.atlassian.com/browse/CONFCLOUD-81975 |
| Large pages | Community reports of slowness | | S https://community.atlassian.com/forums/Confluence-questions/Large-pages-in-the-Cloud-are-slow-to-use-and-worse-when-screen/qaq-p/1531990 |

### 1.16 What users say (all S; review pages could not be opened)

Praised: ease of use, team collaboration, version history, Jira integration. Complained about: search ranks old or duplicate pages above the current one; permissions across site, space and page are hard to reason about; the editor lags on large pages and tables. Stale content is our inference from the missing native review date, not a quoted review. Only four themes on each side could be supported; pricing and table complaints are UNVERIFIED. Sources: https://www.g2.com/products/confluence/reviews, https://www.capterra.com/p/136446/Confluence/reviews/.

## Part 2. What C Note has today (from the code, main at 0277c7c)

Status key: **built**, **partial** (what is missing is stated), **absent**.

### 2.0 Three facts that shape every gap

1. **Markdown is the stored format.** `core.notes.content_md` is the canonical page body; the Yjs document (`content_ydoc`, `core.note_updates`) is seeded from it and snapshotted back to it (`migrations/0001_core.sql:101`, `migrations/0940_durable_docs.sql`). Rich formatting Markdown cannot hold is a small, validated HTML subset: colour spans, aligned `<p>`/`<h1-6>`, and `<table>` for merged or resized tables (`apps/web/src/components/editor/formats.ts`, `html-inline.ts`).
2. **The converter exists twice.** The browser renders Markdown through TipTap's `@tiptap/markdown` plus our `renderMarkdown` overrides; the collab gateway has its own hand-written Markdown-to-Yjs parser and renderer (`apps/collab/src/md.ts`, 1,141 lines). A node the gateway does not know is flattened to its inline text when it snapshots (`md.ts` `render()`, last line), so **any new block must be added to both converters or it is silently destroyed on the next snapshot.**
3. **A page is a Note.** Pages, project items, contacts, invoices and whiteboards are all rows in `core.notes` distinguished by `types` (a page has `types = '{}'`). Sharing, comments, versions, relations and events are therefore shared machinery.

### 2.1 Spaces

| Capability | Status | Evidence |
|---|---|---|
| Create a space (name, key, template, open/private) | built | `notes/write.go` `spaceCreate`; `apps/web/src/components/space-dialog.tsx`; e2e `qa-pages.spec.ts` SP-001/002 |
| Space members with roles admin/editor/viewer | partial: the table and access checks exist, but only the creator is ever inserted (at `space.create`); **no command or UI adds, changes or removes a space member**, so a private space cannot be opened to a colleague | `migrations/0001_core.sql:95` `core.space_members`; `notes/write.go:97`; `apps/api/internal/access/access.go:19-44` |
| Space home | partial: a flat list of the space's pages and a "New page" button; no overview page, description, shortcuts or activity | `apps/web/src/app/w/[ws]/s/[id]/page.tsx` (29 lines) |
| Rename, archive, delete a space; space settings | absent | only `space.create` and `space.list` are registered (`notes/notes.go:21`, `notes/write.go:27`) |
| Space templates (a space pre-filled with pages) | absent | `core.spaces.template` selects a module layout (docs, software, crm), not page content |
| Personal space | absent | |
| Guest role | partial: a workspace `guest` role exists and reads only through explicit page shares; no per-space guest | `command/principal.go:25`; `access/access.go:33`; `notes/write.go:24` |

### 2.2 Page tree and page lifecycle

| Capability | Status | Evidence |
|---|---|---|
| Nested pages (`parent_id`), sidebar tree with fold state remembered | built | `apps/web/src/components/shell.tsx` `Tree`; e2e `tree.spec.ts` |
| Breadcrumb | built | `shell.tsx` `Breadcrumb` |
| Move a page to another parent | partial: `note.update {parent_id}` works with cycle and same-space checks; **no UI** (the e2e test moves through the API); cannot move across spaces | `notes/write.go:320` `checkMove`; `e2e/qa-pages.spec.ts` SP-005 |
| Reorder siblings | absent: there is no position column; the tree shows `updated_at desc`, so pages jump when edited | `notes/write.go:455` |
| Copy / duplicate a page (with children) | absent | |
| Delete a page | partial: `note.delete` soft-deletes (`deleted_at`); **no UI button, no trash list, no restore command** | `notes/write.go:475` |
| Archive and restore | partial: pages are archived only by the freshness sweep (lapsed verification); archived pages can be found and restored from the Docs dashboard; no manual "Archive" action | `worker/freshness.go`; `docs/docs.go:164,200`; `apps/web/src/components/docs/dashboard.tsx:140` |
| Drafts, publish/unpublish | absent: every page is live and autosaved (the Confluence "live doc" model) | `apps/web/src/components/note-editor.tsx` (700 ms debounce) |
| Page owner | built | `doc.set_owner`, owner chip in `apps/web/src/components/docs/note-tools.tsx` |
| Verification with verify-by date, stale/unverified/archived freshness, freshness score per space, AI edit proposals when a bound record changes | built (ahead of Confluence) | `docs/docs.go`; `worker/freshness.go`; e2e `docs-living.spec.ts` |
| Page status (rough draft / in progress / ready for review) | absent: only the freshness pill | |
| Title emoji, cover image, page width | absent: fixed 720 px column | `apps/web/src/app/w/[ws]/n/[id]/page.tsx:219` |
| Folders, whiteboards and tables inside the page tree | absent: the tree lists pages only; whiteboards and Tables have their own sections | `shell.tsx` |
| Tree at scale | partial: the shell loads `note.list {limit: 500}` once; a workspace with more than 500 pages loses the rest from the tree | `shell.tsx` (`note.list`, `limit: 500`) |

### 2.3 Editor

TipTap 3.33 with StarterKit, TextAlign, Color/TextStyle, Image, Table (resizable), TaskList/TaskItem (nested), Placeholder, Markdown, Collaboration and CollaborationCaret (`note-editor.tsx`, `apps/web/package.json`).

| Element | Status | Evidence |
|---|---|---|
| Headings 1-6, paragraph, bold, italic, underline, strike, inline code, link | built | `editor/toolbar.tsx`, `editor/menus.tsx`; e2e `toolbar`, `qa-editor` |
| Text colour (8 colours), alignment (left/centre/right/justify) | built | `toolbar.tsx`; `formats.ts`; e2e `formats.spec.ts` |
| Highlight (background colour), subscript/superscript, indent | absent | |
| Bulleted, numbered, to-do lists (nested) | built | |
| To-do with assignee and due date; task report | absent: a checkbox only | |
| Quote, divider, code block with language | partial: language is kept, but there is no language picker, no syntax highlighting, no line numbers or copy button | `qa-editor.spec.ts` ED-010; no `lowlight` dependency |
| Tables: size picker, add/move/delete rows and columns, sort by column, merge/split, header row toggle, column resize, clear cells | built | `editor/table-controls.tsx`; e2e `table.spec.ts` |
| Tables: cell background colour, numbered column, sticky header, header column, distribute columns | absent | |
| Image | partial: **by URL only** (`http(s)`), no upload, paste or drag-drop, no resize, caption, alignment or alt-text UI | `editor/inserts.tsx:101`; `formats.ts` `SafeImage` |
| Emoji | partial: a fixed grid of plain Unicode; no `:shortcode` typing or search | `editor/inserts.tsx:6` |
| Mention people with `@` | built: stored as a link to the person, notifies in-app | `editor/mention.tsx`; `notes/write.go:366`; e2e `mentions.spec.ts` |
| Mention or link a page with a picker | absent: links are typed URLs through `window.prompt` | `editor/menus.tsx` `promptLink` |
| Slash menu | partial: 16 items (text, H1-3, lists, to-do, table, image, emoji, quote, code, divider, bold, italic, strike, inline code) | `editor/slash.tsx` |
| Fixed toolbar and selection bubble | built | `toolbar.tsx`, `menus.tsx` |
| Markdown shortcuts, paste from web/Word keeping structure, safe paste | built | `qa-editor.spec.ts` ED-002, ED-009, ED-015; `formats.spec.ts` |
| Panels (info/note/warning/success/error), expand, status lozenge, date, table of contents, layouts/columns, decision, anchor | absent | |
| Children list, excerpt/include, content by label, page properties, recently updated | absent | |
| File attachments in the page | absent in the web UI: the backend accepts JPEG/PNG/GIF/WebP/PDF up to 10 MB, 10 per note (`file.upload/list/download/delete`), but nothing in `apps/web/src` calls it | `files/files.go:20-49` |
| Video, smart links, embeds (YouTube, Figma, Google Docs), file preview | absent | |
| Charts, math, diagrams in a page | absent (whiteboards are separate pages) | |
| Embed our own records (project item, invoice, table view, whiteboard) in a page | absent in pages; a whiteboard can embed a project item card | `whiteboard/item-embed.tsx` |
| Template placeholders `{{deal.title}}` | built (see 2.4) | |
| Find and replace, keyboard shortcut help | absent | |

### 2.4 Templates

| Capability | Status | Evidence |
|---|---|---|
| Save a page or board as a template; list, delete | built | `docs/templates.go`; `templates/save-template.tsx`; e2e `templates.spec.ts` |
| Variables bound to CRM/billing/project records (contact, deal, invoice, item fields, workspace name, today, user name), with re-render (`template.refresh`) | built (ahead of Confluence, whose variables are typed in by hand) | `docs/templates.go:27-43` |
| Starter gallery | partial: two seeded templates per workspace (Proposal, Invoice cover letter); no gallery of meeting notes, SOP, policy and so on; the picker is a plain list without preview or categories | `auth/accounts.go:314`; `templates/use-template.tsx` |
| Free-text template variables and instructional placeholder text | absent | |
| Space-scoped templates | built: a template saved from a page is visible to readers of that space | `migrations/0700_templates.sql` |

### 2.5 Collaboration

| Capability | Status | Evidence |
|---|---|---|
| Real-time co-editing with cursors and presence avatars; durable update log; offline edits kept in the tab | built | `apps/collab/src/server.ts`; `components/presence.tsx`; e2e `collab.spec.ts`; `apps/collab/test/resilience.test.ts` |
| Inline comments on selected text, replies, resolve and reopen; highlight follows the text | built | `editor/inline-comments.ts`; `comments-panel.tsx`; `migrations/0930_inline_comments.sql`; e2e `inline-comments.spec.ts`, `qa-pages` CM-002/003 |
| Page-level comments | built | `comments-panel.tsx:220` |
| Comment edit/delete, reactions on comments or pages, likes | absent | |
| Mentions in pages and comments notify | built (in-app bell only) | `notes/write.go:382`; `components/notifications.tsx` |
| Notify the page owner or thread participants of a new comment or reply | absent: `comment.add` notifies mentioned people only | `notes/write.go:651-661` |
| Watch a page or space | absent | |
| Email or digest for page activity | absent: notification kinds are mention, approval, reminder, assigned, quote_decided, chat_mention, lead, site_abuse; all in-app | `migrations/1310_site_abuse.sql:43`; `notify/notify.go` |
| Page activity history as sentences | built | `note.history`; `lib/activity.ts`; e2e `activity.spec.ts` |
| Page analytics (views, viewers) | absent: no view event is recorded | |
| "My tasks" across pages | absent | |

### 2.6 History

| Capability | Status | Evidence |
|---|---|---|
| Automatic versions (checkpoint at most every 10 minutes while editing), immutable | built | `notes/write.go:343-360`; `migrations/0940_durable_docs.sql` |
| View a version, restore (with before-restore safety version and an epoch fence that reloads open editors) | built | `revisions/`; `versions-panel.tsx`; e2e `versions.spec.ts` |
| Compare two versions / highlight changes | absent for versions; a word diff exists for AI proposals (`diff` package, `docs/note-tools.tsx`) and can be reused | |
| Named versions / change notes | absent: `reason` is edit, before_restore or restore | |

### 2.7 Permissions and sharing

| Capability | Status | Evidence |
|---|---|---|
| Page restrictions: none / edit-restricted / view-restricted, inherited down the tree, lock icon in the tree | built | `migrations/0800_page_permissions.sql` `core.note_access`; `sharing/sharing.go`; `share/share-dialog.tsx`; e2e `share.spec.ts` |
| Share a page with a member at view or edit; "Shared with me" | built | `note.share`, `note.shared_with_me` |
| Share with a group or team | absent | |
| Public link to a wiki page | absent: `share.create` accepts quotations, invoices and receipts only | `publiclinks/links.go:108-145` |
| Request access | absent: an unreadable page shows "does not exist or you do not have access" | `n/[id]/page.tsx:128` |
| Copy link | built | `share-dialog.tsx` `copy` |

### 2.8 Search

| Capability | Status | Evidence |
|---|---|---|
| Full-text search with title fuzzy match, snippets, permission filter in the query | built | `notes/write.go:757` `search.notes` |
| Hybrid (semantic) search when the AI module is on | built | `search.hybrid`; `app/w/[ws]/search/page.tsx` |
| Quick find (Cmd/Ctrl+K) | built | `components/palette.tsx` |
| Filters by space, owner/contributor, date, type, label; title-only | absent: `search.notes` takes `q`, `limit`, `include_archived` only, and the results page has no filter bar | |
| Labels and label pages | absent | |
| Recent pages | partial: Home shows the 8 most recently **updated** pages in the workspace, not the ones you viewed | `components/home/page.tsx:38` |
| Starred / favourites | absent | |
| Query language for pages | partial: CNQL and saved views (`core.views`) exist for project items and Tables, not for pages | `projects/cnql.go`; `notes/write.go:837` |

### 2.9 Import and export

| Capability | Status | Evidence |
|---|---|---|
| Import pages from Confluence (storage-format HTML, including macros to plain equivalents) and Notion exports, Trello, Jira, CSV | built | `importers/run.go:31-37`, `importers/html.go` (handles `ac:structured-macro`), `importers/others.go` |
| Import Word (.docx), Google Docs, a folder of Markdown files | absent (pasting from Word and Docs keeps structure) | |
| Export a page to PDF, Word, Markdown or HTML; export a space | absent | no export or print code under the docs UI |

### 2.10 AI

| Capability | Status | Evidence |
|---|---|---|
| Summarise a page, extract tasks, draft with AI | built | `ai.summarize`, `ai.extract_tasks`, `ai.draft`; `components/ai/ai-dialog.tsx` |
| Ask a question over the workspace with cited pages; unanswered questions become capture requests routed to an owner | built (ahead) | `docs/ask.go`; e2e `docs-living.spec.ts`, `ask.spec.ts` |
| Capture a page from pasted text | built | `doc.capture` |
| AI proposes an edit when a linked record changes; owner approves from a diff | built (ahead) | `docs/docs.go:280-410` |
| Inline AI in the editor: rewrite, shorten, change tone, fix grammar, translate (Hindi and regional languages) | absent | |
| MCP server for agents | built | `app/w/[ws]/docs/mcp/page.tsx`; `apps/api/internal/mcp` |

### 2.11 Whiteboards and Tables

| Capability | Status | Evidence |
|---|---|---|
| Whiteboard (tldraw 5.6) with live collaboration, templates, PNG export, design frames, project item cards | built | `components/whiteboard/board.tsx`; e2e `whiteboard.spec.ts` |
| Tables: bases, typed fields, records, grid and kanban views, CSV import, links | built (Phase 1a merged; forms and further views are Phase 1b) | `apps/api/internal/modules/tables`; `docs/design/tables.md`; e2e `tables-*.spec.ts` |
| Embed a whiteboard or a table view inside a page | absent | |

### 2.12 Mobile (`apps/mobile`, Expo)

| Capability | Status | Evidence |
|---|---|---|
| List pages by space (flat, 200 most recent), open, create | partial: no tree | `components/pages.tsx:12` |
| Edit | partial: a **plain Markdown text box** saved with `note.update`; no rich rendering, so HTML-subset content shows as raw tags | `components/pages.tsx:82-152` |
| Search | built | `app/(tabs)/search.tsx` |
| Comments on pages, notifications, offline reading, share | absent | |

### 2.13 Admin

| Capability | Status | Evidence |
|---|---|---|
| Append-only event log per note, audit module with export | built | `core.events` (`0001_core.sql`); `modules/audit` |
| MFA, SSO/SAML, sessions | built | `migrations/1150-1201` |
| Approvals engine | built for money documents; not wired to page publishing | `modules/approvals` |
| Page analytics, retention rules for trash, classification labels | absent | |

## Part 3. Side by side

| Area | Confluence Cloud | C Note today | Verdict |
|---|---|---|---|
| Spaces | Purposes, overview page, shortcuts, roles, guests, archive | Create and list only; private spaces cannot add members | Large gap, mostly administration |
| Page tree | Drag reorder, move, copy, trash, archive, folders, mixed content types | Nested tree; move only through the API; no delete, trash or reorder UI | Large gap in everyday actions |
| Page chrome | Status, owner, emoji, cover, width | Owner and verification (stronger); no status, emoji, cover, width | Medium gap |
| Editor basics | Full formatting, tables with colour and sticky header, images with upload | Solid basics and tables; images by URL only | One sharp gap: image upload |
| Editor blocks | Panels, expand, status, date, TOC, layouts, decisions, tasks with assignee | None of these | Largest visible gap |
| Content macros | Children, excerpt, include, synced blocks, content by label, properties report | None | Gap; a few matter (children, labels) |
| Templates | Gallery, hand-filled variables | Record-bound variables (stronger); gallery of two | Gallery gap only |
| Co-editing | Yes | Yes, with a durable log | Parity |
| Comments | Inline and page, resolve, reactions | Inline and page, replies, resolve, reopen; no reactions, edit or delete | Near parity |
| Notifications | Bell and email; watching | Bell for mentions only; no watch, no email | Gap |
| Tasks | Assignee, due date, task list | Checkbox only | Gap |
| History | List, compare, restore, comments | List, view, restore; no compare | Small gap |
| Permissions | Space roles, inherited view restrictions, public links, guests | Page restrictions and shares built; no public link for pages, no space member management | Medium gap |
| Search | CQL, filters, labels, recent, starred | Full-text and semantic; no filters, labels, stars, true recents | Gap |
| Import | Word, Notion | Confluence, Notion, Trello, Jira, CSV | Ahead, except Word |
| Export | PDF, Word, space export | None | Gap |
| AI | Summarise, write, tone, translate, Q&A, definitions | Summarise, tasks, draft, cited Q&A, capture, edit proposals; no inline rewrite or translate | Mixed; ahead on knowledge upkeep |
| Freshness | No native review date | Verify-by dates, stale flags, freshness score, auto-archive | Ahead |
| Whiteboards | Native, with voting, timer, Jira conversion | tldraw board with live collaboration and item cards | Near parity for SMB use |
| Databases | Table, card, board; embed in pages | Tables module (grid, kanban); no embed in pages | Embed gap |
| Automation | Rules on pages | Project automations only | Gap, low urgency |
| Mobile | View, edit, comment, notifications, limited offline | Flat list, raw Markdown editing, search | Large gap |
| Analytics | Views and viewers (paid tiers) | None | Gap |

## Part 4. UX references (Mobbin)

Mobbin returned no usage notice with these results. Notion and other products are used where Confluence screens were not available.

| Area | References | What to take |
|---|---|---|
| Editor toolbar | [Confluence editor with fixed toolbar and AI prompt](https://mobbin.com/screens/f9059d94-0d0e-4c54-8b6e-a72fe2ab5c51?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Confluence editor, byline with owner, status and read time](https://mobbin.com/screens/f3d6c865-feec-4563-839f-1162b0aa8fdc?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Confluence code block with language picker, wrap and copy](https://mobbin.com/screens/f2b238bb-0835-4032-b40f-bb5f48232723?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Byline under the title carries owner, status lozenge and read time; a small floating bar under a code block holds language, wrap, copy, delete |
| Slash and block menus | [Productboard block menu with "Change to" and Callout: Info / Warning / Success](https://mobbin.com/screens/a91410d0-7908-4be9-be91-10e683e06aed?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Subframe selection bubble](https://mobbin.com/screens/6d0d1bd4-6c53-4658-a67d-cb9bc58261f1?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion code block and file tile](https://mobbin.com/screens/fa966f8d-1a15-4105-99b9-e3fd46c5575e?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Group the menu by kind (Text, Lists, Callouts); panels are one "Callout" entry with a type submenu. No true Confluence or Notion slash-menu screen was returned |
| Page tree | [Confluence sidebar: Recent, Starred, Spaces, content tree with emoji and DRAFT lozenges, title filter](https://mobbin.com/screens/f3d6c865-feec-4563-839f-1162b0aa8fdc?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Plane wiki: Collections, Shared, Private, Archived sections](https://mobbin.com/screens/4e470194-2515-466f-a4e6-f8bae56a7bda?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion sidebar: Recents, Favorites, Private, Teamspaces](https://mobbin.com/screens/fb9142f9-c78f-4f2b-82a0-f3e35fb94ec1?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Recent and Starred sit above the tree; a "Search by title" box filters the tree; emoji replaces the file icon; Archived is its own collapsed section |
| Comments | [Productboard comments panel with All / Unresolved / Resolved filter and quoted text](https://mobbin.com/screens/14c7222d-52cf-45c3-9fbe-8367c9eaf518?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion page with comment under the title and selected text](https://mobbin.com/screens/f9b9cd0f-383d-45c0-af2f-893c1b5e612d?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Our panel already matches this shape; add the filter and a reply box per thread stays |
| Version compare | [Plane: version list on the right, "Compare versions", "Restore this version", "Highlight changes" checkbox](https://mobbin.com/screens/afc55a5a-4236-4f12-859d-9d38a55bd06d?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Productboard: full-screen version history with timeline and edit stepper](https://mobbin.com/screens/d79af42b-c355-4c3a-9c38-149a446d68f3?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Full-width read-only view with the list on the right and a "Highlight changes" toggle; restore is one primary button |
| Template gallery | [Productboard templates dialog: category rail, preview cards](https://mobbin.com/screens/f1ccfeb0-8541-4937-ab71-5b6a8a6b3e8b?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Webflow templates: categories and "Select / View details" on hover](https://mobbin.com/screens/dc42000b-523c-4359-a45f-a8d0f40c01bb?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Plane: page templates in settings](https://mobbin.com/screens/5bf8d217-31d2-46bf-b6e4-5d1006d39512?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | Modal with a category rail on the left and cards with a skeleton preview; "Use" and "Preview" on hover |
| Share dialog | [Confluence Share: add people, "General access: Open, anyone in this space, Can edit"](https://mobbin.com/screens/f5974e9f-a51d-4094-92ab-c529c0e54dcb?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Notion invite with "Can comment" and a message](https://mobbin.com/screens/f70f3807-8825-4a81-926f-54447ffa0665?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp), [Gumloop: people, General access, Copy share link](https://mobbin.com/screens/9edc163f-8ec1-4e77-833d-eaff9207348c?utm_source=claude&utm_medium=ai-assistant&utm_source_platform=mcp) | People on top, one "General access" row below, copy link last. Our dialog already follows this; a public-link row slots into "General access" |
