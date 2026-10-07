// SPDX-License-Identifier: AGPL-3.0-or-later
// Translation check: docker compose run --rm i18n
//
// web/locales/*.json are i18next JSON v4 files, en.json being the source.
// For every locale this verifies: no missing or stale keys, the CLDR plural
// forms the language needs, the same {{variables}} as English. It also checks
// that every key used in web/ and in Go code exists in en.json.
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const ROOT = process.env.ROOT || '/src';
const LOCALES = join(ROOT, 'web/locales');
const PLURAL = /_(zero|one|two|few|many|other)$/;
// Values of dynamic key parts, e.g. t(`pc.quality.${id}.label`).
const DYNAMIC = {
  'pc.quality.${id}.label': ['eco', 'standard', 'high', 'max'],
  'pc.quality.${id}.risk': ['eco', 'standard', 'high', 'max'],
  'pc.quality.${q.id}.risk': ['eco', 'standard', 'high', 'max'],
  'phone.mode.${tr.kind}': ['direct', 'relay'],
  'pc.quality.${lower.id}.label': ['eco', 'standard', 'high', 'max'],
};

let errors = 0, warnings = 0;
const err = (m) => { errors++; console.log('ERROR  ' + m); };
const warn = (m) => { warnings++; console.log('warn   ' + m); };

const flatten = (o, p = '', out = {}) => {
  for (const [k, v] of Object.entries(o)) {
    const key = p ? `${p}.${k}` : k;
    if (v && typeof v === 'object') flatten(v, key, out);
    else if (typeof v === 'string') out[key] = v;
    else err(`${key}: value must be a string`);
  }
  return out;
};
const vars = (s) => [...s.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]).sort().join(',');
const base = (k) => k.replace(PLURAL, '');

const files = readdirSync(LOCALES).filter((f) => f.endsWith('.json')).sort();
const locales = {};
for (const f of files) {
  try { locales[f.slice(0, -5)] = flatten(JSON.parse(readFileSync(join(LOCALES, f), 'utf8'))); }
  catch (e) { err(`${f}: invalid JSON (${e.message})`); }
}
const en = locales.en;
if (!en) { err('en.json (source language) is missing'); process.exit(1); }
const enPlurals = new Set(Object.keys(en).filter((k) => PLURAL.test(k)).map(base));
const enVars = (k) => vars(en[k] ?? en[`${base(k)}_other`] ?? '');

for (const [code, dict] of Object.entries(locales)) {
  let cats;
  try { cats = new Intl.PluralRules(code).resolvedOptions().pluralCategories; }
  catch { err(`${code}: unknown language code`); continue; }
  for (const k of Object.keys(en)) {
    if (PLURAL.test(k)) continue;
    if (!(k in dict)) err(`${code}: missing "${k}"`);
  }
  for (const p of enPlurals) {
    for (const c of cats) if (!(`${p}_${c}` in dict)) err(`${code}: missing plural form "${p}_${c}"`);
  }
  for (const [k, v] of Object.entries(dict)) {
    const known = PLURAL.test(k) ? enPlurals.has(base(k)) : k in en;
    if (!known) { err(`${code}: "${k}" is not in en.json (stale key?)`); continue; }
    if (PLURAL.test(k) && !cats.includes(k.match(PLURAL)[1])) warn(`${code}: "${k}" is not a plural form of this language`);
    if (vars(v) !== enVars(k)) err(`${code}: "${k}" uses {{${vars(v)}}}, English uses {{${enVars(k)}}}`);
    if (!v.trim()) warn(`${code}: "${k}" is empty`);
  }
}

// Keys used in the code.
const exists = (k) => k in en || `${k}_other` in en;
const used = new Set();
const scan = (dir, exts, patterns) => {
  for (const ent of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, ent.name);
    if (ent.isDirectory()) { if (!['node_modules', '.git', 'dist', 'locales'].includes(ent.name)) scan(p, exts, patterns); continue; }
    if (!exts.some((e) => ent.name.endsWith(e))) continue;
    const src = readFileSync(p, 'utf8');
    for (const re of patterns) for (const m of src.matchAll(re)) used.add(m[1]);
  }
};
scan(join(ROOT, 'web'), ['.js', '.html'], [
  /\bt\(\s*['"`]([a-zA-Z][\w.${}]*)['"`]/g,
  /data-i18n="([\w.]+)"/g,
  /(?:^|[;"])\s*[\w-]+:([a-z][\w.]+)/gm,
  /\bkey: '([\w.]+)'/g,
]);
scan(join(ROOT, 'internal'), ['.go'], [/"((?:progress|error)\.[A-Za-z.]+)"/g]);
scan(join(ROOT, 'cmd'), ['.go'], [/"((?:progress|error)\.[A-Za-z.]+)"/g]);

for (const k of used) {
  if (!k.includes('.') || /^(https?|data)\b/.test(k)) continue;
  if (k.includes('${')) {
    const values = DYNAMIC[k];
    if (!values) { warn(`dynamic key "${k}" not checked (add it to DYNAMIC)`); continue; }
    for (const v of values) { const kk = k.replace(/\$\{[^}]+\}/, v); if (!exists(kk)) err(`code uses "${kk}", missing in en.json`); }
    continue;
  }
  if (!exists(k)) err(`code uses "${k}", missing in en.json`);
}

console.log(`\n${files.length} locales (${Object.keys(locales).join(', ')}), ${Object.keys(en).length} keys, ${used.size} used in code: ${errors} error(s), ${warnings} warning(s)`);
process.exit(errors ? 1 : 0);
