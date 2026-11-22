---
id: R-PLATFORM-007
title: A failed delivery is retried a bounded number of times, then reported
statement: |
  One email delivery is attempted at most three times, with a growing pause between
  attempts that is randomized so that many failing senders do not retry in a
  synchronized wave. A cancelled request stops the ladder at once instead of letting it
  run on after the caller and the process have given up.
module: platform
entity: email_delivery
category: policy
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/platform/mailer/mailer.go::Mailer.Send
    layer: service
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/platform/mailer/mailer_test.go::TestSendRetriesBoundedAttempts
    - internal/platform/mailer/mailer_test.go::TestSendJitterIsRandomized
    - internal/platform/mailer/mailer_test.go::TestSendStopsOnCancellation
  e2e: null
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Authority deviates from the category default (policy → domain): the retry decision belongs to the delivery adapter, and no domain operation participates. The numbers are the code's, not a phase document's: three total attempts, base 500ms doubling per attempt, jitter added on top of the base (sleep = base·2^attempt + random(0, base·2^attempt), i.e. in [base·2^attempt, 2·base·2^attempt)), no sleep after the last attempt, and the cancellation check before each redial and inside each sleep. The final error returned is the transport error of the last attempt — the refusal surface is a worker-side log line, not an HTTP status (a delivery is booked on the server's background tracker, never on the request path). The mailer suite now pins the ladder: three bounded attempts, a jitter that varies between runs, and a cancelled context that stops the sleep. No refusal: a delivery failure is retried and then logged on the server's background tracker, never answered to a client, so there is no status to name.
