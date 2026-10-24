import { readFileSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(fileURLToPath(new URL('..', import.meta.url)));
export function parseCatalog(text, label = 'catalog') {
  const data = JSON.parse(text);
  if (!data || Array.isArray(data) || typeof data !== 'object')
    throw new Error(`${label}: expected a flat object`);
  const tokens = text.match(/"(?:\\.|[^"\\])*"|[{}\[\]:,]|[^\s{}\[\]:,]+/g) || [];
  const seen = new Set();
  for (let i = 0; i < tokens.length; i++)
    if (tokens[i].startsWith('"') && tokens[i + 1] === ':') {
      const key = JSON.parse(tokens[i]);
      if (seen.has(key)) throw new Error(`${label}: duplicate key ${key}`);
      seen.add(key);
    }
  for (const [key, value] of Object.entries(data)) {
    if (!/^(?:_meta\.languageName|[a-z][a-zA-Z0-9_]*(?:\.[a-z][a-zA-Z0-9_]*)+)$/.test(key))
      throw new Error(`${label}: invalid semantic key ${key}`);
    if (typeof value !== 'string' || !value.trim())
      throw new Error(`${label}: ${key} must be a nonempty string`);
    if (/[{}]/.test(value.replace(/\{\w+\}/g, '')))
      throw new Error(`${label}: malformed placeholder in ${key}`);
  }
  if (!data['_meta.languageName']) throw new Error(`${label}: missing _meta.languageName`);
  return data;
}
const placeholders = (value) =>
  [...new Set([...value.matchAll(/\{(\w+)\}/g)].map((match) => match[1]))].sort().join(',');
export function validateCatalog(data, base, label) {
  for (const [key, value] of Object.entries(data)) {
    if (!Object.hasOwn(base, key)) throw new Error(`${label}: unknown key ${key}`);
    if (placeholders(value) !== placeholders(base[key]))
      throw new Error(`${label}: placeholder mismatch in ${key}`);
  }
  return Object.keys(base).filter((key) => !Object.hasOwn(data, key));
}
export function sourceReferences(text) {
  return [...text.matchAll(/\b(?:t|translate|message|LocalizedError)\(\s*['"]([^'"]+)['"]/g)].map(
    (match) => match[1],
  );
}
export function validateReferences(text, base, label) {
  for (const key of sourceReferences(text))
    if (!Object.hasOwn(base, key)) throw new Error(`${label}: missing source key ${key}`);
}
export function checkLocales() {
  const directory = join(root, 'src/locales');
  const names = readdirSync(directory).filter((name) => name.endsWith('.json'));
  if (!names.includes('en.json')) throw new Error('Missing English base catalog');
  const base = parseCatalog(readFileSync(join(directory, 'en.json'), 'utf8'), 'en.json');
  for (const name of names) {
    if (!/^[a-z]{2,3}(?:-[a-z0-9]+)*\.json$/.test(name))
      throw new Error(`Invalid locale filename ${name}`);
    const data = parseCatalog(readFileSync(join(directory, name), 'utf8'), name);
    const missing = validateCatalog(data, base, name);
    if (missing.length) console.log(`${name}: ${missing.length} keys use English fallback`);
  }
  function walk(dir) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) walk(path);
      else if (/\.tsx?$/.test(entry.name))
        validateReferences(readFileSync(path, 'utf8'), base, path);
    }
  }
  walk(join(root, 'src'));
  console.log(
    `Locale validation passed: ${names.length} catalogs, ${Object.keys(base).length} base keys`,
  );
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    checkLocales();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
