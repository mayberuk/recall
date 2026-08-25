# Security

## Threat model

`recall` reads local session transcripts under `~/.claude/projects/` and writes a stripped,
compressed archive under `~/.local/share/recall/`. Three facts bound what can go wrong:

- **It reads local transcripts, nothing else.** The only input is what Claude Code already wrote
  to disk on this machine during ordinary use.
- **It makes no network calls, ever.** Nothing it does can exfiltrate transcript content; there
  is no vendor in the loop beyond what already received that content during the session itself.
- **It never writes to the session store.** `~/.claude/projects/` is read-only from `recall`'s
  point of view: it is the sole copy of the corpus, and a bug that corrupted it would be
  unrecoverable. All writes go to `recall`'s own archive directory.

Because both the input and the output stay on the machine, the realistic risk is local: a
transcript may contain secrets a session pasted in (tokens, credentials) which `recall`'s archive
would then also contain in stripped form. Treat the archive with the same care as the transcripts
it's derived from.

## Reporting a vulnerability

Open a private security advisory on this repository (`Security` tab → `Report a vulnerability`)
rather than a public issue. Include the version (`recall --version`) and, if the report involves
a specific transcript, a redacted or synthetic reproduction rather than the real content.
