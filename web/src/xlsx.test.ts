import { describe, expect, it } from 'vitest';
import JSZip from 'jszip';
import { readWorkbook, columnName } from './xlsx';
import { xlsx } from '../e2e/office';

describe('Excel reader', () => {
  it('reads values, formats, merges and visible sheets', async () => {
    const zip = await JSZip.loadAsync(await xlsx());
    const sheets = await readWorkbook(zip, 'en-GB');
    expect(sheets.map((s) => s.name)).toEqual(['Budget', 'Notes']);
    const [budget] = sheets;
    const text = (row: number, col: number) => budget.rows[row]?.[col]?.text;
    expect(text(0, 0)).toBe('Item');
    expect(budget.rows[0][0].bold).toBe(true);
    expect(text(0, 2)).toBe('01/01/2025');
    expect(text(1, 0)).toBe('Rent (May)');
    expect(text(1, 1)).toBe('1,234.50');
    expect(budget.rows[1][1].number).toBe(true);
    expect(budget.rows[2]).toBeUndefined();
    expect(text(3, 1)).toBe('1,234.50');
    expect(text(3, 2)).toBe('TRUE');
    expect(budget.rows[4][0].span).toEqual({ rows: 1, cols: 3 });
    expect(budget.rows[4][1].hidden).toBe(true);
    expect(budget.widths[0]).toBe(145);
    expect(budget.trimmed).toBe(false);
  });

  it('names columns like Excel', () => {
    expect([0, 25, 26, 27, 701, 702].map(columnName)).toEqual(['A', 'Z', 'AA', 'AB', 'ZZ', 'AAA']);
  });
});
