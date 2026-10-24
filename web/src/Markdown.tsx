import { useMemo } from 'react';
import MarkdownIt from 'markdown-it';
import { codeLines, codeRows, copyText } from './CodeView';
import { t, useLanguage, getLanguage } from './i18n';

// Markdown shown formatted. HTML written in the file stays text (html: false)
// and links with unsafe schemes are dropped by the parser. Links open in a new
// tab without telling the other site where they came from, and pictures from
// other servers are not loaded: they become links, so opening a file never
// reports to a third party who read it. Code blocks get colours, line numbers
// and a copy button.
let copyLabel = 'Copy';
const escapeHtml = (text: string) =>
  text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
const md = new MarkdownIt({
  html: false,
  linkify: true,
  highlight: (code: string, language: string): string => {
    const lines = codeLines(code, language);
    return (
      '<pre class="code-block"><button type="button" class="code-copy" data-copy>' +
      escapeHtml(copyLabel) +
      '</button><div class="code-view" style="--digits:' +
      String(lines.length).length +
      '">' +
      codeRows(lines) +
      '</div></pre>'
    );
  },
});
const external = (attrs: string) => attrs + ' target="_blank" rel="noopener noreferrer nofollow"';
md.renderer.rules.link_open = (tokens, idx, options, _env, self) => {
  tokens[idx].attrSet('target', '_blank');
  tokens[idx].attrSet('rel', 'noopener noreferrer nofollow');
  return self.renderToken(tokens, idx, options);
};
// Checklists: "- [ ] milk" and "- [x] bread" become tick boxes. Each box
// knows its line in the text, so ticking it can change exactly that line.
md.core.ruler.after('inline', 'tasks', (state) => {
  const tokens = state.tokens;
  for (let i = 2; i < tokens.length; i++) {
    const inline = tokens[i];
    if (inline.type !== 'inline' || tokens[i - 2].type !== 'list_item_open') continue;
    const match = /^\[([ xX])\]\s+/.exec(inline.content);
    const first = inline.children?.[0];
    if (!match || !first || first.type !== 'text' || !inline.map) continue;
    first.content = first.content.replace(/^\[[ xX]\]\s+/, '');
    const box = new state.Token('html_inline', '', 0);
    box.content =
      '<input type="checkbox" class="task-box" data-task-line="' +
      inline.map[0] +
      '"' +
      (match[1] === ' ' ? '' : ' checked') +
      ' disabled> ';
    inline.children!.unshift(box);
    tokens[i - 2].attrJoin('class', 'task-item');
  }
});
md.renderer.rules.image = (tokens, idx) => {
  const token = tokens[idx];
  const src = String(token.attrGet('src') ?? '');
  const label = md.utils.escapeHtml(token.content || src);
  return src
    ? '<a class="md-image"' +
        external(' href="' + md.utils.escapeHtml(src) + '"') +
        '>' +
        label +
        '</a>'
    : label;
};

// Turns the tick box on the given line of a checklist on or off.
export function toggleTask(text: string, line: number) {
  const lines = text.split('\n');
  lines[line] = lines[line]?.replace(
    /^(\s*(?:[-*+]|\d+[.)])\s+)\[([ xX])\]/,
    (_, lead, mark) => lead + (mark === ' ' ? '[x]' : '[ ]'),
  );
  return lines.join('\n');
}

export default function Markdown({
  text,
  onToggle,
}: {
  text: string;
  // Given, tick boxes can be ticked; the line of the box is passed.
  onToggle?: (line: number) => void;
}) {
  useLanguage();
  const language = getLanguage();
  const html = useMemo(() => {
    copyLabel = t('code.copy');
    const rendered = md.render(text);
    return onToggle ? rendered.replaceAll(' disabled> ', '> ') : rendered;
  }, [text, language, !!onToggle]);
  return (
    <div
      className="markdown-body"
      onClick={(event) => {
        const box = (event.target as HTMLElement).closest<HTMLInputElement>('[data-task-line]');
        if (box) {
          event.preventDefault();
          if (onToggle) onToggle(Number(box.dataset.taskLine));
          return;
        }
        const button = (event.target as HTMLElement).closest<HTMLElement>('[data-copy]');
        const block = button?.parentElement;
        if (!button || !block) return;
        const code = [...block.querySelectorAll('.code-text')].map((l) => l.textContent).join('\n');
        copyText(code).then(
          () => {
            button.textContent = t('code.copied');
            window.setTimeout(() => (button.textContent = t('code.copy')), 1500);
          },
          () => {},
        );
      }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}
