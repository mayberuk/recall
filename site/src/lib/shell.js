// A shell tokeniser, small on purpose. The documentation's code samples are
// one-line invocations of one binary, so a general grammar would be a whole
// dependency spent colouring three kinds of word. It marks what a reader
// actually scans for: which command, which flags, which literal string.
//
// Every ink here is one the page already uses. Nothing introduces a fifth.

export const esc = (s) => s
  .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;');

export const unesc = (s) => s
  .replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"')
  .replace(/&#39;/g, "'").replace(/&amp;/g, '&');

const VERBS = new Set([
  'find', 'turns', 'show', 'when', 'doctor', 'guide', 'update', 'mcp',
  'install', 'config', 'list', 'serve',
]);

// Order matters: a comment swallows the rest of its line, and a quoted string
// swallows a `#` inside it, so both are tried before anything else.
const TOKEN = /(#[^\n]*)|("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')|(\|\||&&|[|;]|>>?|<)|((?:^|(?<=\s))--?[A-Za-z][\w-]*)|([A-Za-z_][\w./-]*)|(\s+)/g;

const wrap = (cls, text) => (cls ? `<span class="${cls}">${esc(text)}</span>` : esc(text));

export function highlight(source) {
  return source.split('\n').map((line) => {
    let rest = line;
    let out = '';

    // A prompt is chrome, not an argument: it is marked and then stops
    // counting, so `$ recall find` and `recall find` tokenise identically.
    const prompt = rest.match(/^(\s*)([$#]) /);
    if (prompt) {
      out += esc(prompt[1]) + `<span class="t-p">${prompt[2]}</span> `;
      rest = rest.slice(prompt[0].length);
    }

    let words = 0;
    let command = '';
    out += rest.replace(TOKEN, (m, comment, str, op, flag, word, space) => {
      if (comment) return wrap('t-c', comment);
      if (str) return wrap('t-s', str);
      // A pipe starts a new command, so the word after it is a command again.
      if (op) { words = 0; return wrap('t-o', op); }
      if (flag) return wrap('t-f', flag);
      if (space) return esc(space);
      words++;
      if (words === 1) { command = word; return wrap('t-b', word); }
      if (words === 2 && /^recall/.test(command) && VERBS.has(word)) return wrap('t-v', word);
      return esc(word);
    });
    return out;
  }).join('\n');
}

// Rewrites every fenced block in compiled markdown into a copyable, tokenised
// one. The copy target is the source text rather than the rendered spans,
// because what lands on a clipboard has to be what a shell would accept.
export function codeBlocks(html) {
  return html.replace(
    /<pre(?:[^>]*)><code(?:[^>]*)>([\s\S]*?)<\/code><\/pre>/g,
    (_, inner) => {
      const raw = unesc(inner).replace(/\n$/, '');
      return `<div class="cb"><button type="button" class="cbCopy" data-code="${esc(raw)}" ` +
        `aria-label="Copy this command">Copy</button>` +
        `<pre><code>${highlight(raw)}</code></pre></div>`;
    }
  );
}
