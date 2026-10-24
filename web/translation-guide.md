# Frontend translation guide

English (`src/locales/en.json`) is the base, default and per-key fallback. Swedish is provided in `sv.json`. Catalogs are flat JSON objects with semantic keys (for example `auth.username`, not source sentences or positional IDs). Every catalog must have a nonempty `_meta.languageName`, written in that language.

## Add a language with one file

Add `src/locales/<locale>.json` (for example `de.json`) with `_meta.languageName` and translated English keys. Use lowercase BCP-47 filenames (`pt-br.json` for a regional catalog). Vite's eager `import.meta.glob` discovers the file at build time; no import, language list or other code edit is needed. Rebuild to include it. Partial catalogs are supported: omit untranslated keys, and English is used for each missing key. Do not use empty strings to indicate missing translations. Unknown keys are errors.

Once the repository is published, fork it, add your catalog, run the checks below and submit a pull request containing that language file. The language's native name appears automatically in Settings after the build.

## Translate safely

Preserve each key's placeholder names exactly, such as `{name}`, `{count}`, `{size}` and `{status}`. Placeholders may move or repeat. Values are inserted as plain strings and rendered by React; catalogs must not contain HTML. Never use `dangerouslySetInnerHTML` for translations, API errors, file names, usernames or share titles. Prefer complete sentences with placeholders instead of concatenating translated fragments. User-supplied file names/titles are data, not catalog keys.

Components call `useLanguage()` to subscribe to live changes and use literal `t('semantic.key', {name: value})` calls. Keep new UI text, placeholders, aria labels, notifications, validation, errors and metadata in the catalogs. `formatNumber`, `formatDate` and `formatBytes` use the active language; keep raw numeric values for API payloads and form controls. SI/IEC unit symbols, protocol identifiers and decorative punctuation are not translated. English metadata is injected into the HTML by Vite; document title, description, Open Graph title and `html.lang` update at runtime. The optional branding generator reads its text from the English catalog without changing the approved logo crop or theme.

For stored errors/notices, use `message('semantic.key', values)` with `useMessage()`, or a `LocalizedError` carrying a descriptor, rather than storing `t(...)` strings in state. This lets existing notices retranslate live without resubmitting a request. Nested errors may be descriptor/Error parameters; filenames, titles and unknown server strings remain plain data.

## Language choice

A valid manual choice saved as `magisend-language` overrides browser preferences. Automatic tries supported `navigator.languages` entries in order, with exact regional matching then base matching (`sv-SE` → `sv`), then `navigator.language`, then English. Removing the saved choice selects Automatic. Reads/writes are guarded; if storage is denied, switching still works in the current document. Browser persistence across a full reload cannot be guaranteed when the browser denies storage. Settings is in the global header on anonymous, public and authenticated pages and uses the existing modal and mobile header styles.

## API errors

The backend contract is `{error: English fallback, error_code: stable_key}`. `responseData` looks up `errors.<error_code>` in the selected catalog and English base. Unknown codes retain the backend's English fallback; missing fallback text uses `errors.http`. The current stable keys and English fallback are defined in `backend-errors-contract.json`, which is embedded into the Go binary. All current codes have English and Swedish translations and an exhaustive frontend contract test. New UI languages do not require modifying that backend contract. Do not infer codes from English messages, status numbers or filenames.

## Validation

Run `npm run check:locales`. It uses Node built-ins, no additional dependencies, and tests the validator before checking all locale files and literal translation/descriptor source references. It rejects invalid JSON, duplicate keys (including escaped equivalents), invalid semantic keys, unknown keys, missing language names, non-string/empty values, malformed placeholders and placeholder mismatches. Missing translations are reported and allowed for partial catalogs. English must define every literal source key. Dynamic `errors.<code>` lookup intentionally uses fallback rather than requiring every future backend code.

Run `npm test` and `npm run build` in your development environment with the locked dependencies. Existing Swedish regression tests explicitly select Swedish in `src/test-setup.ts`; production does not default to Swedish. The i18n tests cover browser resolution, fallback, plain-string interpolation, locale formatting, settings persistence/live switching/storage denial, public/authenticated/admin translation, upload/API errors and UTF-8 validation. Web-setup tests cover tokenless one-time setup and recovery after failed automatic login. Use a separate data directory for browser tests; never create test administrators in a real installation.
