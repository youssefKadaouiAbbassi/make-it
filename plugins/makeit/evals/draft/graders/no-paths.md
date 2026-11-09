---
type: regex
target: last_message
match: not_contains
pattern: "(\\b[\\w-]+\\.(js|ts|json|md|txt|yml|yaml|sh|py|go)\\b|(^|\\s)(\\./|~/|/|src/)[\\w.-]+|exportInvoices)"
weight: 2
---
