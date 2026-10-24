import type JSZip from 'jszip';
import { unsupported } from './officeFile';

// A small reader for Excel workbooks (.xlsx): cell values as Excel last
// calculated them, number and date formats, bold text, merged cells and
// column widths. Formulas are never evaluated and macros never read.
export interface Cell {
  text: string;
  number: boolean;
  bold: boolean;
  span?: { rows: number; cols: number };
  // Covered by a merged cell to its left or above.
  hidden?: boolean;
}
export interface Sheet {
  name: string;
  rows: Cell[][];
  widths: number[];
  // The sheet holds more than is shown.
  trimmed: boolean;
}

export const maxRows = 2000;
export const maxCols = 100;

function xml(text: string) {
  const doc = new DOMParser().parseFromString(text, 'application/xml');
  if (doc.getElementsByTagName('parsererror').length) throw unsupported();
  return doc;
}
const all = (root: Document | Element, name: string) => [...root.getElementsByTagNameNS('*', name)];
const first = (root: Document | Element, name: string) =>
  root.getElementsByTagNameNS('*', name)[0] as Element | undefined;
const children = (root: Element | undefined, name: string) =>
  [...(root?.children ?? [])].filter((e) => e.localName === name);

async function part(zip: JSZip, path: string) {
  const file = zip.file(path.replace(/^\//, ''));
  return file ? xml(await file.async('string')) : null;
}

function columnIndex(ref: string) {
  let n = 0;
  for (const ch of ref.toUpperCase().match(/^[A-Z]+/)?.[0] ?? '')
    n = n * 26 + ch.charCodeAt(0) - 64;
  return n - 1;
}
function position(ref: string) {
  const row = Number(ref.match(/\d+$/)?.[0] ?? 0) - 1;
  return { row, col: columnIndex(ref) };
}
export function columnName(index: number) {
  let name = '';
  for (let n = index + 1; n > 0; n = Math.floor((n - 1) / 26))
    name = String.fromCharCode(65 + ((n - 1) % 26)) + name;
  return name;
}

// Text of a shared or inline string, rich text included; phonetic hints are not.
function stringText(si: Element) {
  return all(si, 't')
    .filter((t) => t.parentElement?.localName !== 'rPh')
    .map((t) => t.textContent ?? '')
    .join('');
}

const builtinDates = new Set([14, 15, 16, 17, 18, 19, 20, 21, 22, 45, 46, 47]);
const builtinFormats: Record<number, string> = {
  1: '0',
  2: '0.00',
  3: '#,##0',
  4: '#,##0.00',
  9: '0%',
  10: '0.00%',
  11: '0.00E+00',
  37: '#,##0',
  38: '#,##0',
  39: '#,##0.00',
  40: '#,##0.00',
  49: '@',
};

interface Style {
  format: string;
  date: boolean;
  bold: boolean;
}

function isDateFormat(code: string) {
  const bare = code.replace(/"[^"]*"|\[[^\]]*\]|\\./g, '');
  return /[dmyhs]/i.test(bare) && !/^[#0.,%E+\- ]*$/.test(bare);
}

function formatNumber(value: number, style: Style | undefined, date1904: boolean, locale: string) {
  if (style?.date) {
    const days = value + (date1904 ? 1462 : 0);
    const ms = Math.round((days - 25569) * 86400 * 1000);
    if (!Number.isFinite(ms)) return String(value);
    const when = new Date(ms);
    const timeOnly = value < 1;
    const hasTime = /[hs]/i.test(style.format.replace(/"[^"]*"|\[[^\]]*\]/g, ''));
    return new Intl.DateTimeFormat(locale, {
      timeZone: 'UTC',
      ...(timeOnly ? {} : { dateStyle: 'short' }),
      ...(hasTime || timeOnly ? { timeStyle: 'short' } : {}),
    }).format(when);
  }
  const code = (style?.format ?? 'General').split(';')[0];
  if (code === 'General' || code === '@' || !/[0#]/.test(code)) {
    return Math.abs(value) >= 1e11 || (value !== 0 && Math.abs(value) < 1e-9)
      ? value.toExponential(5)
      : String(Number(value.toPrecision(11)));
  }
  if (/E\+/i.test(code)) return value.toExponential((code.split('.')[1]?.match(/0/g) ?? []).length);
  const percent = code.includes('%');
  const decimals = (code.split('.')[1]?.match(/[0#]/g) ?? []).length;
  const shown = new Intl.NumberFormat(locale, {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
    useGrouping: code.includes(','),
  }).format(percent ? value * 100 : value);
  return percent ? shown + '%' : shown;
}

export async function readWorkbook(zip: JSZip, locale: string): Promise<Sheet[]> {
  const workbook = await part(zip, 'xl/workbook.xml');
  if (!workbook) throw unsupported();
  const date1904 = ['1', 'true'].includes(
    first(workbook, 'workbookPr')?.getAttribute('date1904') ?? '',
  );
  const relations = await part(zip, 'xl/_rels/workbook.xml.rels');
  const targets = new Map(
    all(relations ?? workbook, 'Relationship').map((r) => [
      r.getAttribute('Id'),
      r.getAttribute('Target') ?? '',
    ]),
  );
  const shared = await part(zip, 'xl/sharedStrings.xml');
  const strings = shared ? children(shared.documentElement, 'si').map(stringText) : [];

  const stylesDoc = await part(zip, 'xl/styles.xml');
  const formats = new Map<number, string>();
  for (const f of all(stylesDoc ?? workbook, 'numFmt'))
    formats.set(Number(f.getAttribute('numFmtId')), f.getAttribute('formatCode') ?? 'General');
  const fonts = stylesDoc
    ? children(first(stylesDoc, 'fonts'), 'font').map((f) => {
        const b = children(f, 'b')[0];
        return !!b && !['0', 'false'].includes(b.getAttribute('val') ?? '');
      })
    : [];
  const styles: Style[] = stylesDoc
    ? children(first(stylesDoc, 'cellXfs'), 'xf').map((xf) => {
        const id = Number(xf.getAttribute('numFmtId') ?? 0);
        const format = formats.get(id) ?? builtinFormats[id] ?? 'General';
        return {
          format,
          date: builtinDates.has(id) || (formats.has(id) && isDateFormat(format)),
          bold: fonts[Number(xf.getAttribute('fontId') ?? 0)] ?? false,
        };
      })
    : [];

  const sheets: Sheet[] = [];
  for (const entry of all(workbook, 'sheet')) {
    if (entry.getAttribute('state') === 'hidden' || entry.getAttribute('state') === 'veryHidden')
      continue;
    const id =
      entry.getAttributeNS(
        'http://schemas.openxmlformats.org/officeDocument/2006/relationships',
        'id',
      ) ?? entry.getAttribute('r:id');
    let target = targets.get(id) ?? '';
    if (!target) continue;
    target = target.startsWith('/') ? target.slice(1) : 'xl/' + target;
    const doc = await part(zip, target);
    if (!doc) continue;
    const rows: Cell[][] = [];
    let trimmed = false;
    let lastRow = -1;
    for (const row of all(doc, 'row')) {
      const r = row.getAttribute('r');
      const index = r ? Number(r) - 1 : lastRow + 1;
      lastRow = index;
      if (index >= maxRows) {
        trimmed = true;
        break;
      }
      let lastCol = -1;
      for (const c of children(row, 'c')) {
        const ref = c.getAttribute('r');
        const col = ref ? columnIndex(ref) : lastCol + 1;
        lastCol = col;
        if (col >= maxCols) {
          trimmed = true;
          continue;
        }
        const type = c.getAttribute('t') ?? 'n';
        const style = styles[Number(c.getAttribute('s') ?? 0)];
        const raw = first(c, 'v')?.textContent ?? '';
        let text = raw;
        let number = false;
        if (type === 's') text = strings[Number(raw)] ?? '';
        else if (type === 'inlineStr') text = stringText(first(c, 'is') ?? c);
        else if (type === 'b') text = raw === '1' ? 'TRUE' : 'FALSE';
        else if (type === 'n' && raw !== '') {
          number = true;
          text = formatNumber(Number(raw), style, date1904, locale);
        }
        (rows[index] ??= [])[col] = { text, number, bold: style?.bold ?? false };
      }
    }
    for (const merge of all(doc, 'mergeCell')) {
      const [from, to] = (merge.getAttribute('ref') ?? '').split(':');
      if (!from || !to) continue;
      const a = position(from);
      const b = position(to);
      if (a.row >= maxRows || a.col >= maxCols) continue;
      const rowsSpan = Math.min(b.row, maxRows - 1) - a.row + 1;
      const colsSpan = Math.min(b.col, maxCols - 1) - a.col + 1;
      if (rowsSpan < 1 || colsSpan < 1 || rowsSpan * colsSpan > 100000) continue;
      for (let y = a.row; y < a.row + rowsSpan; y++)
        for (let x = a.col; x < a.col + colsSpan; x++)
          if (y !== a.row || x !== a.col)
            (rows[y] ??= [])[x] = { text: '', number: false, bold: false, hidden: true };
      const top = ((rows[a.row] ??= [])[a.col] ??= { text: '', number: false, bold: false });
      top.span = { rows: rowsSpan, cols: colsSpan };
    }
    const widths: number[] = [];
    for (const col of all(doc, 'col')) {
      const min = Number(col.getAttribute('min') ?? 1) - 1;
      const max = Math.min(Number(col.getAttribute('max') ?? 1) - 1, maxCols - 1);
      const width = Number(col.getAttribute('width') ?? 0);
      for (let i = min; i <= max; i++)
        widths[i] = col.getAttribute('hidden') === '1' ? 0 : Math.round(width * 7 + 5);
    }
    sheets.push({ name: entry.getAttribute('name') ?? '', rows, widths, trimmed });
  }
  if (!sheets.length) throw unsupported();
  return sheets;
}
