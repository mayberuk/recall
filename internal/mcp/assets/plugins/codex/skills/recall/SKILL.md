---
name: recall
description: Recalls what was said, decided, or tried in past coding-agent sessions on this machine, and reaches for that record before re-deriving something a past session already settled; use it when a request sounds like what did we decide about a topic, where did we leave off, have we hit this before, which session was that, or when did we first raise something.
---

# recall

recall searches the transcripts of past coding-agent sessions on this machine. One
query reaches every checkout of a repo, not just the one the caller is standing in.
Reach for it before re-deriving something a past session already settled, rather than
guessing at an answer this machine has already produced once.

## The first answer teaches

The first recall_find, recall_turns, recall_show or recall_when call of a session
already carries a compact guide ahead of its own answer: how a query is read, what is
searched by default, and the footer contract. That happens once per server process, not
once per call, so there is no round trip to make before a first search.

recall_guide returns the same material at full length (every command, every argument,
the recipes) and stays there to call directly when the compact page already in context
is not enough.

## Which tool answers which question

- **recall_find**: which session talked about something, and how much. Reach for it
  for "which session was that" and "have we hit this before".
- **recall_turns**: the passages themselves, ranked across every session at once.
  Reach for it for "what did we decide about X" and "what did we actually say".
- **recall_show**: recovers one named session with the turns around a match, for
  reading a conclusion in context. Takes the session id recall_find or recall_turns
  returned.
- **recall_when**: places a topic in time: first said, last said, and the months
  between. Reach for it for "when did we first raise this" and "where did we leave
  off".
- **recall_guide**: the semantics themselves: how a query is read, what a search
  leaves out by default, and what every argument narrows.

## What is searched by default

Only the conversation tier is searched. Tool output is most of the store by volume and
is only searched with `results: true`; tool invocation lines need `tools: true`. Scope
defaults to the current repo; pass `all: true` to reach every repo on the machine. The
asking session's own turns and recall's own past output are excluded by default,
because both would answer the question with itself.

Every one of these narrowings shows up in the coverage footer every answer carries. If
the footer does not name a narrowing, that narrowing did not happen. Read the footer
before deciding a search came back empty rather than merely scoped.

## How a query is read

Terms are ANDed. A query that no turn carries in full does not come back empty; it
degrades to the turns carrying the most of it, and the footer names which terms those
were. `all_terms: true` requires every term and returns nothing rather than a partial
match. `not` skips turns carrying a given term and is repeatable.

Matching is case-insensitive and matches inside words: `build` finds `iosBuild`, and a
camelCase or acronym boundary ranks that match as a whole word, not a lesser inside
match. A term no turn carries may be corrected to a one-edit neighbor the corpus
actually has, and the footer names the substitution; a neighbor two edits away is only
ever suggested, never substituted. A small shipped synonym table also searches the
other spelling of some terms (`auth`/`authentication`, `db`/`database`, and a few dozen
more), matched only as a whole word, and the footer names the table and its version
when it fires. The table is curated and shipped, never learned from a corpus, so the
same query answers the same way on any machine. `exact: true` turns off stem expansion,
near-neighbor correction and the synonym table alike.

## Answer size

A searching tool call that names no `budget` gets one anyway: 4000 tokens by default, so
an answer never lands unshaped in a session's context. An explicit `budget` always wins,
and the footer names `--budget` when it shaped the answer.

## Session ids

A session id matches on any unique prefix, so the short form recall_find or recall_turns
returns is enough for recall_show's `session` argument; the full id is never required.
