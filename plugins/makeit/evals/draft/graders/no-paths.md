---
type: regex
target: last_message
match: not_contains
pattern: "([\\w-]+/[\\w.-]+|\\b[\\w-]+\\.(js|ts|json|md|txt|yml|yaml|sh|py|go)\\b|exportInvoices)"
weight: 2
---
