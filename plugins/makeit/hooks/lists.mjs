// lists.mjs: options.sh's check of a reply's ending, asked of marked, the parser Claude Code
// renders with: it numbers an ordered list as its start plus each item's place. Reads the reply on
// stdin and prints one word: "joined" when it ends on numbered options that show another number
// than the one written on them, "options" when it ends on options that show as written, "none"
// when it does not end on them (the awk then reads the ending as before), "hidden" when every
// marked line is inside code or HTML, where no choices show at all. With no ordered list holding a
// marked item but a marked line in ordinary text (options written with bold numbers, "**1.**
// Release", are no list in Markdown) it prints nothing: the awk judges them. Exits 3 with no output when no marked is found; options.sh then runs its awk passes.
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
// Lines written at the margin right under the last option belong to it for marked; the awk reads
// them as lines after the options.
if (at >= 0) lines.push(...tokens[at].items.at(-1).raw.split('\n').slice(1).filter((l) => /^\S/.test(l)));
if (at < 0) {
  if (!tokens.some((t) => !['code', 'html'].includes(t.type) && t.raw.split('\n').some(marked))) console.log('hidden');
} else if (after.some((t) => t.type === 'list') || lines.length > 2) {
  console.log('none');
} else {
  const list = tokens[at];
  const start = list.start === '' ? 1 : list.start;
  // The options begin after the last empty line above the last marked item (an item's raw ends
  // in it), or at the last item numbered lower than the one above it (a plain line joined them).
  const num = (it) => Number(/^ *(\d+)/.exec(it.raw)[1]);
  const last = list.items.findLastIndex((it) => marked(it.raw));
  let first = 0;
  for (let i = 1; i <= last; i++) if (/\n\s*\n\s*$/.test(list.items[i - 1].raw) || num(list.items[i]) < num(list.items[i - 1])) first = i;
  const wrote = num(list.items[first]);
  console.log(start + first === wrote ? 'options' : 'joined');
}
