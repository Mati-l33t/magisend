import JSZip from 'jszip';

// Small but real Word and Excel files for the viewer tests.
const rels = (items: [string, string, string][]) =>
  '<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">' +
  items
    .map(([id, type, target]) => `<Relationship Id="${id}" Type="${type}" Target="${target}"/>`)
    .join('') +
  '</Relationships>';
const office = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships/';

export async function docx(heading: string, text: string, link: string) {
  const zip = new JSZip();
  zip.file(
    '[Content_Types].xml',
    '<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>',
  );
  zip.file('_rels/.rels', rels([['rId1', office + 'officeDocument', 'word/document.xml']]));
  zip.file(
    'word/_rels/document.xml.rels',
    rels([
      ['rId5', office + 'hyperlink', link],
      ['rId6', office + 'styles', 'styles.xml'],
    ]).replace('/>', ' TargetMode="External"/>'),
  );
  zip.file(
    'word/document.xml',
    `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="${office.slice(0, -1)}"><w:body>` +
      `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>${heading}</w:t></w:r></w:p>` +
      `<w:p><w:r><w:t>${text}</w:t></w:r></w:p>` +
      `<w:p><w:hyperlink r:id="rId5"><w:r><w:t>a link</w:t></w:r></w:hyperlink></w:p>` +
      '<w:tbl><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4"/><w:bottom w:val="single" w:sz="4"/><w:insideH w:val="single" w:sz="4"/></w:tblBorders></w:tblPr><w:tblGrid><w:gridCol w:w="3000"/><w:gridCol w:w="3000"/></w:tblGrid>' +
      '<w:tr><w:tc><w:p><w:r><w:t>Month</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Sales</w:t></w:r></w:p></w:tc></w:tr>' +
      '<w:tr><w:tc><w:p><w:r><w:t>May</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>42</w:t></w:r></w:p></w:tc></w:tr></w:tbl>' +
      '<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr>' +
      '</w:body></w:document>',
  );
  zip.file(
    'word/styles.xml',
    '<?xml version="1.0" encoding="UTF-8"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">' +
      '<w:docDefaults><w:rPrDefault><w:rPr><w:sz w:val="22"/></w:rPr></w:rPrDefault></w:docDefaults>' +
      '<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>' +
      '<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:color w:val="2E74B5"/><w:sz w:val="40"/></w:rPr></w:style>' +
      '</w:styles>',
  );
  return zip.generateAsync({ type: 'nodebuffer' });
}

export async function xlsx() {
  const zip = new JSZip();
  zip.file(
    '[Content_Types].xml',
    '<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>',
  );
  zip.file('_rels/.rels', rels([['rId1', office + 'officeDocument', 'xl/workbook.xml']]));
  zip.file(
    'xl/workbook.xml',
    `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="${office.slice(0, -1)}"><sheets><sheet name="Budget" sheetId="1" r:id="rId1"/><sheet name="Secret" sheetId="2" state="hidden" r:id="rId2"/><sheet name="Notes" sheetId="3" r:id="rId3"/></sheets></workbook>`,
  );
  zip.file(
    'xl/_rels/workbook.xml.rels',
    rels([
      ['rId1', office + 'worksheet', 'worksheets/sheet1.xml'],
      ['rId2', office + 'worksheet', 'worksheets/sheet2.xml'],
      ['rId3', office + 'worksheet', 'worksheets/sheet3.xml'],
    ]),
  );
  zip.file(
    'xl/sharedStrings.xml',
    '<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>Item</t></si><si><t>Cost</t></si><si><r><t>Rent</t></r><r><t xml:space="preserve"> (May)</t></r></si><si><t>Total</t></si></sst>',
  );
  zip.file(
    'xl/styles.xml',
    '<?xml version="1.0" encoding="UTF-8"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><numFmts count="1"><numFmt numFmtId="164" formatCode="yyyy-mm-dd"/></numFmts><fonts count="2"><font/><font><b/></font></fonts><cellXfs count="4"><xf numFmtId="0" fontId="0"/><xf numFmtId="0" fontId="1"/><xf numFmtId="4" fontId="0"/><xf numFmtId="164" fontId="0"/></cellXfs></styleSheet>',
  );
  const sheet = (body: string, extra = '') =>
    `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><cols><col min="1" max="1" width="20" customWidth="1"/></cols><sheetData>${body}</sheetData>${extra}</worksheet>`;
  zip.file(
    'xl/worksheets/sheet1.xml',
    sheet(
      '<row r="1"><c r="A1" t="s" s="1"><v>0</v></c><c r="B1" t="s" s="1"><v>1</v></c><c r="C1" s="3"><v>45658</v></c></row>' +
        '<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" s="2"><v>1234.5</v></c></row>' +
        '<row r="4"><c r="A4" t="s" s="1"><v>3</v></c><c r="B4" s="2"><f>SUM(B2:B3)</f><v>1234.5</v></c><c r="C4" t="b"><v>1</v></c></row>' +
        '<row r="5"><c r="A5" t="inlineStr"><is><t>Merged note</t></is></c></row>',
      '<mergeCells count="1"><mergeCell ref="A5:C5"/></mergeCells>',
    ),
  );
  zip.file(
    'xl/worksheets/sheet2.xml',
    sheet('<row r="1"><c r="A1" t="inlineStr"><is><t>hidden</t></is></c></row>'),
  );
  zip.file(
    'xl/worksheets/sheet3.xml',
    sheet('<row r="1"><c r="A1" t="inlineStr"><is><t>Second sheet</t></is></c></row>'),
  );
  return zip.generateAsync({ type: 'nodebuffer' });
}
