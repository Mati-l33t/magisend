import { useEffect, useState } from 'react';
import { LoaderCircle } from 'lucide-react';
import { t, useLanguage, getLanguage, formatNumber, LocalizedError } from './i18n';
import { officeBytes, openArchive, unsupported } from './officeFile';
import { readWorkbook, columnName, maxRows, maxCols, type Sheet } from './xlsx';

const xlsxType = 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet';

// Shows an Excel workbook as tables, one sheet at a time, with the values
// Excel last calculated.
export default function SheetView({ url, failed }: { url: string; failed: (e: Error) => void }) {
  useLanguage();
  const [sheets, setSheets] = useState<Sheet[] | null>(null);
  const [current, setCurrent] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setSheets(null);
    setCurrent(0);
    (async () => {
      const zip = await openArchive(await officeBytes(url, xlsxType, controller.signal));
      const read = await readWorkbook(zip, getLanguage());
      if (!cancelled) setSheets(read);
    })().catch((e) => {
      if (!cancelled) failed(e instanceof LocalizedError ? e : unsupported());
    });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [url]);
  if (!sheets)
    return (
      <div className="office-view">
        <p role="status">
          <LoaderCircle className="spin" size={18} /> {t('preview.loading')}
        </p>
      </div>
    );
  const sheet = sheets[Math.min(current, sheets.length - 1)];
  // Rows are sparse: empty rows have no entry at all.
  const columns = sheet.rows.reduce((n, row) => Math.max(n, row?.length ?? 0), 1);
  const rows = sheet.rows.length;
  return (
    <div className="office-view sheet-view">
      {sheets.length > 1 && (
        <div className="sheet-tabs" role="tablist" aria-label={t('preview.sheets')}>
          {sheets.map((s, i) => (
            <button
              key={i}
              role="tab"
              aria-selected={i === current}
              className={i === current ? 'active' : ''}
              onClick={() => setCurrent(i)}
            >
              {s.name}
            </button>
          ))}
        </div>
      )}
      <div className="sheet-scroll" role="tabpanel" aria-label={sheet.name}>
        <table className="sheet-table">
          <colgroup>
            <col className="sheet-corner-col" />
            {Array.from({ length: columns }, (_, i) => (
              <col key={i} style={{ width: (sheet.widths[i] ?? 72) + 'px' }} />
            ))}
          </colgroup>
          <thead>
            <tr>
              <th className="sheet-corner" aria-hidden="true" />
              {Array.from({ length: columns }, (_, i) => (
                <th key={i} scope="col">
                  {columnName(i)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {Array.from({ length: rows }, (_, r) => (
              <tr key={r}>
                <th scope="row">{r + 1}</th>
                {Array.from({ length: columns }, (_, c) => {
                  const cell = sheet.rows[r]?.[c];
                  if (cell?.hidden) return null;
                  return (
                    <td
                      key={c}
                      rowSpan={cell?.span?.rows}
                      colSpan={cell?.span?.cols}
                      className={
                        (cell?.number ? 'number' : '') + (cell?.bold ? ' bold' : '') || undefined
                      }
                    >
                      {cell?.text}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {sheet.trimmed && (
        <p className="muted">
          {t('preview.sheetTrimmed', {
            rows: formatNumber(maxRows),
            cols: formatNumber(maxCols),
          })}
        </p>
      )}
    </div>
  );
}
