// lists.mjs: options.sh's check of a reply's ending, asked of marked, the parser Claude Code
// renders with: it numbers an ordered list as its start plus each item's place. Reads the reply on
// stdin and prints one word: "joined" when it ends on numbered options that show another number
// than the one written on them, "options" when it ends on options that show as written, "none"
// when it does not end on them. With no ordered list holding a marked item it prints nothing
// when a marked line sits in ordinary text (options written with bold numbers, "**1.** Release",
// are no list in Markdown: the awk judges them), and "none" when every one is inside code or HTML,
// where no choices show at all. Exits 3 with no output when no marked is found; options.sh then runs its awk passes.
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { homedir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import { pathToFileURL } from 'node:url';

// The marked folder: makeit's npm prefix (lib/node_modules on Linux and macOS, node_modules under
// the prefix on Windows, where makeit's prefix is ~/.local/bin), then a marked program on PATH.
function markedDir() {
  const prefixes = [process.env.MAKEIT_NPM_PREFIX, join(homedir(), '.local'), join(homedir(), '.local', 'bin')];
  for (const p of prefixes.filter(Boolean)) {
    for (const d of [join(p, 'lib', 'node_modules', 'marked'), join(p, 'node_modules', 'marked')]) {
      if (existsSync(join(d, 'package.json'))) return d;
    }
  }
  if (process.env.MAKEIT_NPM_PREFIX) return null; // a test's prefix is the only one looked at
  for (const p of (process.env.PATH || '').split(delimiter)) {
    try {
      const d = dirname(dirname(realpathSync(join(p, 'marked'))));
      if (existsSync(join(d, 'package.json'))) return d;
    } catch { /* not here */ }
  }
  return null;
}

// The awk's marker rule (options.sh marked()), on any line of an item.
const marked = (l) =>
  /\(recommended\)|\(recommand(é|É)\)|· recommended|· recommand(é|É)/i.test(l) || /[*][*][^*]+[*][*]\s*·/.test(l);
// A choice laid out like the picker: "1. **Label**", with nothing after but " · …".
const picker = (l) => /^ *\d+[.)] +\*\*[^*]+\*\*( *·.*)?$/.test(l);

const dir = markedDir();
if (!dir) process.exit(3);
const pkg = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'));
const entry = pkg.exports?.['.']?.import?.default ?? pkg.exports?.['.']?.import ?? pkg.module ?? pkg.main;
const { marked: md } = await import(pathToFileURL(join(dir, typeof entry === 'string' ? entry : 'lib/marked.esm.js')));

let text = '';
for await (const chunk of process.stdin) text += chunk;

// The ending, as options.sh's awk reads it: the last ordered list holding a marked item, followed
// by at most two lines outside code, quotes and a resume command, and by no other list.
const tokens = md.lexer(text);
const at = tokens.findLastIndex((t) => t.type === 'list' && t.ordered && t.items.some((it) => marked(it.raw)));
const after = tokens.slice(at + 1).filter((t) => !['space', 'code', 'blockquote'].includes(t.type));
const lines = after.flatMap((t) => t.raw.split('\n')).filter((l) => l.trim() !== '' && !l.includes('claude --resume'));
if (at < 0) {
  if (!tokens.some((t) => !['code', 'html'].includes(t.type) && t.raw.split('\n').some(marked))) console.log('none');
} else if (after.some((t) => t.type === 'list') || lines.length > 2) {
  console.log('none');
} else {
  const list = tokens[at];
  const start = list.start === '' ? 1 : list.start;
  const first = list.items.findIndex((it) => marked(it.raw) || picker(it.raw.split('\n')[0]));
  const wrote = Number(/^ *(\d+)/.exec(list.items[first].raw)[1]);
  console.log(start + first === wrote ? 'options' : 'joined');
}
