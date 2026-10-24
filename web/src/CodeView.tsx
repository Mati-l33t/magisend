import { useMemo } from 'react';
import hljs from 'highlight.js/lib/core';
export { copyText } from './clipboard';
import bash from 'highlight.js/lib/languages/bash';
import c from 'highlight.js/lib/languages/c';
import cpp from 'highlight.js/lib/languages/cpp';
import csharp from 'highlight.js/lib/languages/csharp';
import css from 'highlight.js/lib/languages/css';
import diff from 'highlight.js/lib/languages/diff';
import dockerfile from 'highlight.js/lib/languages/dockerfile';
import go from 'highlight.js/lib/languages/go';
import ini from 'highlight.js/lib/languages/ini';
import java from 'highlight.js/lib/languages/java';
import javascript from 'highlight.js/lib/languages/javascript';
import json from 'highlight.js/lib/languages/json';
import kotlin from 'highlight.js/lib/languages/kotlin';
import lua from 'highlight.js/lib/languages/lua';
import markdown from 'highlight.js/lib/languages/markdown';
import nginx from 'highlight.js/lib/languages/nginx';
import php from 'highlight.js/lib/languages/php';
import powershell from 'highlight.js/lib/languages/powershell';
import python from 'highlight.js/lib/languages/python';
import ruby from 'highlight.js/lib/languages/ruby';
import rust from 'highlight.js/lib/languages/rust';
import sql from 'highlight.js/lib/languages/sql';
import swift from 'highlight.js/lib/languages/swift';
import typescript from 'highlight.js/lib/languages/typescript';
import xml from 'highlight.js/lib/languages/xml';
import yaml from 'highlight.js/lib/languages/yaml';

const languages = {
  bash,
  c,
  cpp,
  csharp,
  css,
  diff,
  dockerfile,
  go,
  ini,
  java,
  javascript,
  json,
  kotlin,
  lua,
  markdown,
  nginx,
  php,
  powershell,
  python,
  ruby,
  rust,
  sql,
  swift,
  typescript,
  xml,
  yaml,
};
for (const [name, language] of Object.entries(languages)) hljs.registerLanguage(name, language);

const byExtension: Record<string, string> = {
  js: 'javascript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  py: 'python',
  go: 'go',
  rs: 'rust',
  c: 'c',
  h: 'c',
  cpp: 'cpp',
  cc: 'cpp',
  hpp: 'cpp',
  cs: 'csharp',
  java: 'java',
  kt: 'kotlin',
  swift: 'swift',
  rb: 'ruby',
  php: 'php',
  lua: 'lua',
  sh: 'bash',
  bash: 'bash',
  zsh: 'bash',
  ps1: 'powershell',
  sql: 'sql',
  css: 'css',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'ini',
  ini: 'ini',
  conf: 'ini',
  xml: 'xml',
  html: 'xml',
  htm: 'xml',
  md: 'markdown',
  markdown: 'markdown',
  diff: 'diff',
  patch: 'diff',
};

// Colouring very large files would hold up the page; they keep line numbers
// but stay uncoloured.
const highlightLimit = 300_000;
const escape = (text: string) =>
  text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

// highlight.js returns escaped text and <span class="…"> tags only. Spans can
// run over several lines (comments, strings), so each line closes what is
// open and the next one opens it again.
function splitLines(html: string) {
  const lines: string[] = [];
  const open: string[] = [];
  let line = '';
  for (const part of html.split(/(<span[^>]*>|<\/span>|\n)/)) {
    if (part === '\n') {
      lines.push(line + '</span>'.repeat(open.length));
      line = open.join('');
    } else if (part.startsWith('<span')) {
      open.push(part);
      line += part;
    } else if (part === '</span>') {
      open.pop();
      line += part;
    } else line += part;
  }
  lines.push(line);
  return lines;
}

export function codeLines(text: string, language: string) {
  const known = language && hljs.getLanguage(language) && text.length <= highlightLimit;
  const html = known
    ? hljs.highlight(text, { language, ignoreIllegals: true }).value
    : escape(text);
  const lines = splitLines(html);
  // A final line break does not start an extra empty line.
  if (lines.length > 1 && text.endsWith('\n')) lines.pop();
  return lines;
}

// The lines as HTML rows; line numbers are drawn by CSS, so copying the code
// never copies them.
export function codeRows(lines: string[]) {
  return lines
    .map((l) => '<div class="code-line"><span class="code-text">' + l + '</span></div>')
    .join('');
}

export default function CodeView({
  text,
  language,
  wrap,
}: {
  text: string;
  language: string;
  wrap: boolean;
}) {
  const name = byExtension[language] || language;
  const lines = useMemo(() => codeLines(text, name), [text, name]);
  return (
    <div
      className={'code-view' + (wrap ? ' wrap' : '')}
      style={{ '--digits': String(lines.length).length } as React.CSSProperties}
      dangerouslySetInnerHTML={{ __html: codeRows(lines) }}
    />
  );
}
