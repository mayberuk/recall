---
description: Reach for recall — search past coding-agent sessions on this machine — before re-deriving something a past session already settled; use it when a request sounds like what did we decide about a topic, where did we leave off, have we hit this before, which session was that, or when did we first raise something.
trigger: model_decision
---

# recall

Reach for the recall MCP tools (recall_find, recall_turns, recall_show, recall_when,
recall_guide) or the `recall` command line before re-deriving something a past session
already settled.

Call recall_guide (or `recall guide`) first — it explains how a query is read and what
is searched by default. Only the conversation tier is searched unless `results` is set;
scope defaults to the current repo unless `all` is set. Session ids match on any unique
prefix. Terms are ANDed, and a long query degrades to the best partial match rather
than returning nothing — the answer's footer says which terms those were.
