// Package reminders sends automatic payment reminders for unpaid invoices (docs/design/collections.md).
//
// A worker sweep (every 5 minutes) runs once per workspace per IST day after 10:00 IST. It picks the invoices whose
// due date matches one of the workspace's enabled steps today (IST calendar dates; a missed day is NOT back-filled),
// writes a billing.reminder_log row (unique per invoice and step, so a step never goes out twice) and enqueues the
// mail in the same transaction. The mail.send job rechecks eligibility right before sending and marks the log
// sent. Delivery is at-least-once: a crash between the SMTP send and the commit repeats the same Message-ID.
// A mail whose job exhausted its retries is marked failed by the next sweep. Reminders are operational only: they
// create no accounting document.
package reminders
