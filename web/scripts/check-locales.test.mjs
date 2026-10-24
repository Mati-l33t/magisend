import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseCatalog, validateCatalog, validateReferences } from './check-locales.mjs';
const base = { '_meta.languageName': 'English', 'test.message': 'Hello {name}' };
test('rejects invalid JSON, duplicate decoded keys, empty values and nested values', () => {
  for (const text of [
    '{',
    '{"_meta.languageName":"en","x.key":"a","x.key":"b"}',
    '{"_meta.languageName":"en","x.key":"a","x.\\u006bey":"b"}',
    '{"_meta.languageName":" "}',
    '{"_meta.languageName":"en","x.key":{}}',
  ])
    assert.throws(() => parseCatalog(text));
});
test('checks semantic keys and placeholder validity and equivalence', () => {
  assert.throws(() => parseCatalog('{"_meta.languageName":"en","bad":"value"}'));
  assert.throws(() => parseCatalog('{"_meta.languageName":"en","x.key":"Hello {"}'));
  assert.throws(() => validateCatalog({ ...base, 'test.message': 'Hello {other}' }, base, 'test'));
  assert.throws(() => validateCatalog({ ...base, 'test.unknown': 'unknown' }, base, 'test'));
  assert.deepEqual(validateCatalog({ '_meta.languageName': 'Partial' }, base, 'test'), [
    'test.message',
  ]);
});
test('checks every literal source key reference', () => {
  validateReferences('t(\'test.message\');translate("test.message")', base, 'source');
  assert.throws(() => validateReferences("t('test.missing')", base, 'source'));
});
test('checks stored translation descriptors and localized exception keys', () => {
  validateReferences("message('test.message');new LocalizedError('test.message')", base, 'source');
  assert.throws(() => validateReferences("message('test.missing')", base, 'source'));
  assert.throws(() => validateReferences("new LocalizedError('test.missing')", base, 'source'));
});
