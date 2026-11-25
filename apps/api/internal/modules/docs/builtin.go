package docs

import "strings"

// Built-in page templates (docs-parity 5.5, 7.6). They are code, not rows, so a wording fix reaches every workspace.
// Ids are "builtin:<key>". Bodies use the stored block forms of 5.1 (panel, expand, status, toc) and only the common
// placeholders (the meeting notes also ask for a date and the attendees), so each renders with nothing unresolved except prompts and reads sensibly as plain text where a block is unknown.
// The web tests read the raw string literals of this file: keep backticks for bodies only.

const builtinPrefix = "builtin:"

type builtin struct {
	Key, Name, Category, Description, Title, Body string
}

// builtinKey returns the key of a built-in id; ok is false for every other id (which then goes on to UUID parsing).
func builtinKey(id string) (string, bool) { return strings.CutPrefix(id, builtinPrefix) }

func findBuiltin(key string) (builtin, bool) {
	for _, b := range builtins {
		if b.Key == key {
			return b, true
		}
	}
	return builtin{}, false
}

// Categories in gallery order; custom templates are shown under "Your templates" by the client.
var builtinCategories = []string{"Meetings", "Operations", "HR", "Sales", "Projects"}

var builtins = []builtin{
	{"meeting-notes", "Meeting notes (MoM)", "Meetings", "Minutes with attendees, decisions and action items.", "Meeting notes {{today}}", `> [!INFO]
> Date: {{ask.date}}. Minutes taken by {{user.name}} for {{workspace.name}}.

## Attendees

- {{user.name}}
- {{ask.attendees}}

## Agenda

1. Review of last week's actions
2. Main topic
3. Any other business

## Discussion

Write the key points here, one short paragraph per topic.

## Decisions

> [!SUCCESS]
> Record what was agreed, so nobody has to ask again.

## Action items

| Task | Owner | Due | Status |
| --- | --- | --- | --- |
| Share the revised quotation | Owner name | Date | <span data-status="yellow">IN PROGRESS</span> |
| Confirm the delivery schedule | Owner name | Date | <span data-status="grey">TO DO</span> |

## Next meeting

Date, time and place (or meeting link).
`},
	{"sop", "Standard operating procedure", "Operations", "A step-by-step procedure with owner, safety notes and checks.", "SOP: process name", `[[toc]]

## Purpose

Why this procedure exists and what it protects: quality, safety, money or customer trust.

## Scope

Who follows it, and where it applies (shop floor, office, warehouse, delivery).

## Owner and review

| Item | Detail |
| --- | --- |
| Process owner | {{user.name}} |
| Prepared on | {{today}} |
| Review every | 6 months |

## Before you start

> [!WARNING]
> List safety and compliance points here. Anything that can hurt a person or cause a loss goes in this box.

## Steps

1. First step, written as an instruction.
2. Second step.
3. Third step.

## Checks

- [ ] Output matches the specification
- [ ] Records are updated the same day
- [ ] Supervisor has signed off

## If something goes wrong

<details><summary>Who to call and what to do</summary>

Name the person to inform first, the backup, and the fix for the two most common problems.

</details>
`},
	{"company-policy", "Company policy", "Operations", "A policy with purpose, rules, exceptions and acknowledgement.", "Policy: topic", `[[toc]]

> [!NOTE]
> Applies to all employees of {{workspace.name}}. Effective from {{today}}.

## Purpose

State in two lines why the company needs this policy.

## Who it covers

Permanent staff, contract staff, trainees and visitors, as applicable.

## The policy

1. First rule, stated plainly.
2. Second rule.
3. Third rule.

## What is not allowed

> [!ERROR]
> List the actions that lead to disciplinary action.

## Exceptions

Exceptions need written approval from the director or the HR head.

## Approval and review

| Item | Detail |
| --- | --- |
| Approved by | Name and designation |
| Version | 1.0 |
| Next review | Date |

## Acknowledgement

- [ ] I have read and understood this policy ({{user.name}}, {{today}})
`},
	{"employee-onboarding", "Employee onboarding checklist", "HR", "Joining formalities, accounts, training and 30-60-90 day goals.", "Onboarding: employee name", `> [!INFO]
> Joining date: {{today}}. HR contact: {{user.name}}.

## Before day one

- [ ] Signed offer letter and joining form received
- [ ] Aadhaar, PAN and bank details collected
- [ ] PF and ESI details collected, if applicable
- [ ] Email, chat and laptop ready

## Day one

- [ ] Welcome by the manager and a tour of the office
- [ ] Introduction to the team
- [ ] Attendance and leave process explained
- [ ] Company policies shared for acknowledgement

## First week

| Task | Owner | Status |
| --- | --- | --- |
| Role and targets discussed | Manager | <span data-status="grey">TO DO</span> |
| Tool training | Team lead | <span data-status="grey">TO DO</span> |
| Buddy assigned | HR | <span data-status="grey">TO DO</span> |

## 30, 60 and 90 days

<details><summary>Goals for the first three months</summary>

- 30 days: learn the work, meet the people
- 60 days: handle tasks on your own
- 90 days: review with the manager and confirm the role

</details>
`},
	{"leave-policy", "Leave and attendance policy", "HR", "Leave types, holidays, attendance rules and approval flow.", "Leave and attendance policy", `[[toc]]

> [!NOTE]
> Effective from {{today}}. Questions go to HR.

## Working hours

Standard hours are 10:00 to 18:30, Monday to Saturday. Update to match your office.

## Leave types

| Leave | Days per year | Notes |
| --- | --- | --- |
| Casual leave | 8 | Up to 2 days at a time |
| Sick leave | 6 | Medical certificate after 2 days |
| Earned leave | 15 | Apply 7 days ahead |
| Maternity and paternity | As per law | Contact HR |

## How to apply

1. Apply in the leave module, with the reason.
2. The manager approves or declines within 2 working days.
3. HR updates the attendance record.

## Holidays

> [!INFO]
> Add the holiday list for the year: Republic Day, Holi, Independence Day, Diwali and your regional festivals.

## Late arrival and absence

- Three late arrivals in a month count as one half day.
- Absence without information is treated as loss of pay.
`},
	{"project-plan", "Project plan", "Projects", "Goal, scope, milestones, risks and who does what.", "Project plan: name", `[[toc]]

## Goal

One sentence on what success looks like, and by when.

## Summary

| Item | Detail |
| --- | --- |
| Sponsor | Name |
| Project lead | {{user.name}} |
| Start date | {{today}} |
| Budget | ₹ amount |
| Status | <span data-status="blue">PLANNING</span> |

## Scope

- In scope: what we will deliver
- Out of scope: what we will not do

## Milestones

| Milestone | Owner | Due | Status |
| --- | --- | --- | --- |
| Requirements signed off | Name | Date | <span data-status="grey">TO DO</span> |
| First version ready | Name | Date | <span data-status="grey">TO DO</span> |
| Go live | Name | Date | <span data-status="grey">TO DO</span> |

## Risks

> [!WARNING]
> List what could delay the project: vendor delays, festival-season staff leave, payment holds.

## Updates

<details><summary>Weekly status log</summary>

Add one line per week: what moved, what is blocked.

</details>
`},
	{"decision-record", "Decision record", "Projects", "Why a decision was made, the options and what changes.", "Decision: short title", `> [!INFO]
> Decided on {{today}} by {{user.name}}. Status: proposed, accepted or replaced.

## Context

What problem or opportunity made this decision necessary?

## Options considered

| Option | Cost | Benefit | Risk |
| --- | --- | --- | --- |
| Option A | ₹ amount | What we gain | What could go wrong |
| Option B | ₹ amount | What we gain | What could go wrong |
| Do nothing | None | No effort | The problem stays |

## Decision

> [!SUCCESS]
> We choose option A, because of the reasons below.

## Reasons

- Reason one
- Reason two

## What changes

- [ ] First follow-up action, with owner
- [ ] Second follow-up action, with owner

## Review date

Date on which we check whether this was the right call.
`},
	{"how-to-guide", "How-to guide", "Operations", "Instructions for one task, with tips and common problems.", "How to: task", `> [!NOTE]
> Written by {{user.name}} on {{today}}. Time needed: about 15 minutes.

## What you will need

- Access or login for the tool
- The document or file to work on

## Steps

1. Open the tool and sign in.
2. Do the first thing, in one clear sentence.
3. Do the second thing.
4. Check the result against the example below.

## Example

Show a real example, such as a filled-in GST invoice or a UPI payment screen.

## Common problems

<details><summary>It did not work. What now?</summary>

Describe the most likely cause and the fix. If it still fails, tell the person who owns this guide.

</details>

<details><summary>Who can approve this?</summary>

Name the approver and how to reach them.

</details>
`},
	{"weekly-report", "Weekly team report", "Meetings", "Done, next, blockers and numbers for the week.", "Weekly report: week ending {{today}}", `> [!INFO]
> Team report by {{user.name}} for {{workspace.name}}, week ending {{today}}.

## Highlights

- The most important win this week
- A number that moved

## Done this week

| Task | Owner | Status |
| --- | --- | --- |
| Completed task | Name | <span data-status="green">DONE</span> |
| Task still in progress | Name | <span data-status="yellow">IN PROGRESS</span> |

## Planned for next week

- [ ] Priority one
- [ ] Priority two
- [ ] Priority three

## Blockers

> [!WARNING]
> What is stuck, who is needed to unblock it, and by when.

## Numbers

| Measure | This week | Last week |
| --- | --- | --- |
| Orders | 0 | 0 |
| Collections (₹) | 0 | 0 |
| Open complaints | 0 | 0 |
`},
	{"client-brief", "Client requirements brief", "Sales", "What the client wants, budget, timeline and approvals.", "Requirements brief: client name", `[[toc]]

> [!INFO]
> Prepared by {{user.name}} on {{today}}. Share with the client for sign-off.

## Client

| Item | Detail |
| --- | --- |
| Company | Client name |
| Contact person | Name, phone, email |
| GSTIN | 22AAAAA0000A1Z5 |
| Brief status | <span data-status="blue">DRAFT</span> |

## What they need

Describe the problem in the client's own words.

## Requirements

- Must have: the essentials
- Good to have: extras if time and budget allow

## Budget and timeline

| Item | Detail |
| --- | --- |
| Budget | ₹ amount, plus 18% GST |
| Payment terms | 50% advance, 50% on delivery |
| Deadline | Date |

## Assumptions

> [!WARNING]
> Anything not written here is not included. Confirm each assumption with the client.

## Sign-off

- [ ] Client has reviewed this brief
- [ ] Changes agreed in writing
`},
	{"vendor-evaluation", "Vendor evaluation", "Operations", "Compare suppliers on price, quality, delivery and terms.", "Vendor evaluation: item or service", `> [!NOTE]
> Evaluation by {{user.name}} on {{today}}. Get at least three written quotations before deciding.

## What we are buying

Item or service, quantity, specification and required delivery date.

## Comparison

| Criteria | Vendor A | Vendor B | Vendor C |
| --- | --- | --- | --- |
| Price (₹, excl. GST) | 0 | 0 | 0 |
| GST registered | Yes | Yes | Yes |
| Delivery time | Days | Days | Days |
| Payment terms | Terms | Terms | Terms |
| Quality or samples | Rating | Rating | Rating |
| References | Names | Names | Names |

## Checks

- [ ] GSTIN verified on the GST portal
- [ ] Sample or demo reviewed
- [ ] Two customer references called
- [ ] Warranty and return terms in writing

## Recommendation

> [!SUCCESS]
> Name the chosen vendor and the main reason.

## Approval

| Approver | Decision | Date |
| --- | --- | --- |
| Name | <span data-status="grey">PENDING</span> | Date |
`},
	{"sales-call-notes", "Sales call notes", "Sales", "Call summary, needs, objections and the agreed next step.", "Sales call: customer name", `> [!INFO]
> Call on {{today}}, handled by {{user.name}}.

## Who we spoke to

| Item | Detail |
| --- | --- |
| Customer | Company name |
| Person | Name and role |
| Phone | Number |
| Lead stage | <span data-status="blue">QUALIFIED</span> |

## What they need

- The problem they described
- Their current supplier or tool
- When they plan to buy

## Objections

<details><summary>Price, timing and trust</summary>

Write what they said and how we answered.

</details>

## Quote discussed

| Item | Quantity | Rate (₹) |
| --- | --- | --- |
| Product or service | 0 | 0 |

## Next step

> [!SUCCESS]
> Agree one clear next step, with a date: send the quotation, visit the site or schedule a demo.

- [ ] Follow up on WhatsApp
- [ ] Send the quotation by email
`},
}
