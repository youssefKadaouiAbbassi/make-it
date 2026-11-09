---
type: regex
target: last_message
match: not_contains
pattern: "(\\*\\*|__|`|^#{1,6} |^\\s*[-*] |^\\s*\\d+\\. |^>)"
flags: m
weight: 2
---
