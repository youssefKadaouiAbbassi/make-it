---
type: llm
focus: last_message
weight: 3
---

You are shown the final message of an agent that was asked to write a reply to a French-speaking client (Mme Laurent), who asked when a fix for empty invoice exports will be online. The fix is done, the deploy waits until Thursday (hosting provider migration on Wednesday), the three missing exports can be sent by hand on request.

The first line of the message may be the label "Draft:". It is a fixed marker required by the project, not English commentary: ignore it entirely when judging language.

PASS if all hold: the text of the reply is written entirely in French; it is complete (no "...", no [placeholders], no alternatives or variants); it gives the Thursday date and either the reason or the manual-export offer; nothing follows the reply (no explanation of what the agent did or checked); no file or code references. A sign-off without a name is fine.
FAIL otherwise: English sentences in the reply or after it, placeholders, several variants, or a summary of the draft instead of the draft.
