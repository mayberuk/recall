#!/usr/bin/env zsh
#
# recall-fzf — an fzf front end over the recall CLI. It runs recall's own
# commands and renders their output; it never searches, ranks or parses
# transcripts itself.
#
# Install: source it. There is nothing to build and nothing to set up per
# project.
#
#     source ~/dev/recall/shell/recall.zsh
#
# Use:
#     recall-fzf [--ids] <query> [extra recall find flags...]
#
#     recall-fzf agvtool                  # live finder, prints the chosen session id
#     recall-fzf agvtool --all            # extra flags go straight to recall find
#     recall show "$(recall-fzf agvtool)" # composes, because the id is all it prints
#
# With no terminal the same pipeline runs under `fzf --filter` and prints the
# ranked records to stdout, so an agent that cannot drive a finder still reaches
# results through this code path:
#
#     recall-fzf agvtool | head          # ranked records, blank line between them
#     recall-fzf --ids agvtool           # one session id per line

: ${RECALL_BIN:=recall}

# Reading the format flag from the environment means a different spelling in
# the shipped CLI costs a variable here rather than an edit.
: ${RECALL_FZF_FORMAT_FLAG:=--fzf}

recall-fzf() {
  emulate -L zsh
  setopt local_options pipe_fail

  local ids=0
  [[ ${1-} == --ids ]] && { ids=1; shift }

  local query=${1-}
  (( $# > 0 )) && shift
  [[ ${1-} == -- ]] && shift
  local -a extra=("$@")

  local bin=$RECALL_BIN
  if ! command -v -- "$bin" > /dev/null 2>&1 && [[ ! -x $bin ]]; then
    print -ru2 -- "recall-fzf: $bin not found; set RECALL_BIN to the built binary"
    return 127
  fi

  # A developer's FZF_DEFAULT_OPTS carries its own --preview and --color; during
  # the spike a bat-based one hijacked this invocation and errored on every row.
  # Cleared for the child only, and every flag this function depends on is
  # passed explicitly below rather than left to command-line-wins precedence.
  local -x FZF_DEFAULT_OPTS= FZF_DEFAULT_OPTS_FILE=

  local -a find_cmd=("$bin" find "$query" "${extra[@]}" "$RECALL_FZF_FORMAT_FLAG")

  # recall puts a zero-hit report and its coverage line on stderr, and the
  # records on stdout, so an empty list is only explained by the stderr side.
  local notefile
  # An explicit template rather than `mktemp -t <prefix>`: -t takes a bare
  # prefix on BSD but demands a template of at least three X's on GNU
  # coreutils, so the BSD spelling fails outright on Linux.
  notefile=$(mktemp "${TMPDIR:-/tmp}/recall-fzf.XXXXXX") || return 1

  local bin_q=${(q)bin}
  local fmt_q=${(q)RECALL_FZF_FORMAT_FLAG}
  local extra_q=${(j: :)${(q)extra}}
  local note_q=${(q)notefile}
  local keys='enter: id   ctrl-o: full session   ctrl-/: preview   esc: quit'

  local -a common=(
    # --read0 without --print0 makes fzf separate records with the same newline
    # that appears inside one, so a consumer cannot tell one record from two.
    --read0
    --print0
    --delimiter=$'\x1f'
    # Not cosmetic. Without it recall's colour codes render as literal text in
    # the finder and leak raw escapes into --filter output.
    --ansi
  )

  local -a ui=(
    --with-nth=2..
    --id-nth=1
    # --track follows a record's identity only when --id-nth is paired with a
    # *synchronous* reload; under async change:reload it silently degrades to
    # tracking a screen index and lands on the wrong record. A find is ~35 ms,
    # so blocking the keystroke is not felt. Do not "modernise" this to reload.
    --track
    --bind="change:reload-sync(if [ -n {q} ]; then $bin_q find {q} $extra_q $fmt_q 2> $note_q; else : > $note_q; fi)"
    # The coverage line is a contract: a search that does not declare the tier it
    # skipped is a defect. On a hit recall folds it into the last record, but a
    # zero-hit query has no record to carry it, so the header shows the note
    # instead — once the result is final, not on every intermediate keystroke.
    --bind="result-final:transform-header([ \"\$FZF_MATCH_COUNT\" = 0 ] && [ -s $note_q ] && cat $note_q; printf '%s' ${(q)keys})"
    # recall owns matching and ranking; fzf only draws.
    --disabled
    --no-sort
    --no-multi
    --gap=1
    --height=100%
    --layout=reverse
    --query="$query"
    --prompt='recall> '
    --header="$keys"
    # Field 1 is hidden from the rows by --with-nth but stays addressable, which
    # is the only reason it is a field at all.
    --accept-nth=1
    --preview="$bin_q show {1}"
    --preview-window='right,55%,border-left,wrap'
    --bind='ctrl-/:toggle-preview'
    # 4 MiB sits deliberately above the largest conversation in the corpus
    # (2.35 MB): --full refuses rather than truncates, so a tighter cap turns
    # this key into an error on exactly the sessions worth opening. It is safe
    # only because ctrl-o pipes into a human's pager — nothing on this path
    # reaches an agent's context, which is what the bounded-output dealbreaker
    # protects. Do not copy this number to a path that returns text to a caller.
    --bind="ctrl-o:execute($bin_q show {1} --full --max-bytes 4194304 | \${PAGER:-less} -R)"
  )

  local -i rc=0 forward=0
  local -a ps

  {
    if ( exec < /dev/tty ) 2> /dev/null; then
      local sel=
      if [[ -n $query ]]; then
        "${find_cmd[@]}" 2> "$notefile" | fzf "${common[@]}" "${ui[@]}" |
          IFS= read -r -d $'\0' sel
      else
        # No query means no results to ask recall for yet; the first keystroke
        # fires the reload that fills the list.
        : | fzf "${common[@]}" "${ui[@]}" | IFS= read -r -d $'\0' sel
      fi
      ps=("${pipestatus[@]}")
      rc=${ps[2]}
      # A miss is already on screen in the header, so only a producer that
      # actually failed is worth repeating to a caller after the finder exits.
      (( ps[1] )) && { rc=${ps[1]}; forward=1 }
      [[ -n $sel ]] && print -r -- "$sel"
    elif [[ -z $query ]]; then
      print -ru2 -- "recall-fzf: a query is required when there is no terminal"
      rc=2
    else
      local rec
      # No fzf on this path. With an empty filter it neither matches nor ranks —
      # recall already did both — so its only job was splitting the record on
      # \x1f, and it does not do that reliably: fzf 0.67 ignores --accept-nth
      # under --filter entirely and hands back the whole record for any field
      # asked for. Splitting here is the same work with no version to be wrong
      # about, and it keeps recall's concentration ranking, which re-ranking
      # under a non-empty filter would have thrown away.
      "${find_cmd[@]}" 2> "$notefile" |
        while IFS= read -r -d $'\0' rec; do
          if (( ids )); then
            print -r -- "${rec%%$'\x1f'*}"
          else
            print -r -- "${rec#*$'\x1f'}"
            print
          fi
        done
      ps=("${pipestatus[@]}")
      # recall's own exit code is the answer now that nothing downstream can
      # invent one: 1 is "searched, matched nothing", which is what a caller
      # needs to tell apart from a broken archive.
      rc=${ps[1]}
      # There is no header without a terminal, so the miss report and its
      # coverage line reach the caller the only way left: stderr.
      forward=1
    fi
  } always {
    (( forward )) && [[ -s $notefile ]] && print -ru2 -- "$(<$notefile)"
    rm -f -- "$notefile"
  }

  return rc
}
