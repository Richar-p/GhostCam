// SPDX-License-Identifier: AGPL-3.0-or-later
'use strict';
// Minimal i18next-compatible translations, shared by the phone page and the PC
// window. Files: web/locales/<lang>.json in i18next JSON v4 format (nested
// keys, {{var}} interpolation, CLDR plural suffixes _one/_few/_many/_other…),
// so they can be managed with Weblate, Crowdin, Lokalise, etc.
// en.json is the source language and the fallback for missing keys.
//
// Markup: data-i18n="key" sets textContent; data-i18n-attr="attr:key;attr:key"
// sets attributes (aria-label, title, alt…).

const I18N = (() => {
  let lang = 'en';
  let dict = {};
  let fallback = {};
  let available = ['en'];

  const lookup = (d, key) => key.split('.').reduce((o, k) => (o && typeof o === 'object' ? o[k] : undefined), d);
  const has = (key) => typeof lookup(dict, key) === 'string' || typeof lookup(fallback, key) === 'string';

  async function load(code) {
    const r = await fetch(`/locales/${code}.json`, { cache: 'no-cache' });
    if (!r.ok) throw new Error(`locale ${code}: ${r.status}`);
    return r.json();
  }

  // pick returns the best available locale for the preferred tags
  // (exact match first, then the base language: "pt-BR" -> "pt").
  function pick(tags) {
    for (const tag of tags.filter(Boolean)) {
      const l = tag.toLowerCase();
      const hit = available.find((a) => a.toLowerCase() === l) || available.find((a) => a.toLowerCase() === l.split('-')[0]);
      if (hit) return hit;
    }
    return 'en';
  }

  async function init(preferred) {
    try { available = await (await fetch('/locales', { cache: 'no-cache' })).json(); } catch {}
    lang = pick([preferred, ...(navigator.languages || [navigator.language])]);
    fallback = await load('en');
    dict = lang === 'en' ? fallback : await load(lang).catch(() => fallback);
    document.documentElement.lang = lang;
    apply(document);
    return lang;
  }

  function t(key, vars = {}) {
    let k = key;
    if (typeof vars.count === 'number') {
      const cat = new Intl.PluralRules(lang).select(vars.count);
      k = [cat, 'other'].map((c) => `${key}_${c}`).find(has) || key;
    }
    let s = lookup(dict, k);
    if (typeof s !== 'string') s = lookup(fallback, k);
    if (typeof s !== 'string') return key;
    return s.replace(/\{\{\s*(\w+)\s*\}\}/g, (m, v) => (v in vars ? String(vars[v]) : m));
  }

  function apply(root) {
    root.querySelectorAll('[data-i18n]').forEach((el) => { el.textContent = t(el.dataset.i18n); });
    root.querySelectorAll('[data-i18n-attr]').forEach((el) => {
      for (const pair of el.dataset.i18nAttr.split(';')) {
        const [attr, key] = pair.split(':').map((x) => x.trim());
        if (attr && key) el.setAttribute(attr, t(key));
      }
    });
  }

  // Locale-aware number, e.g. num(1.5) -> "1.5" (en) / "1,5" (fr).
  const num = (n, digits = 1) => new Intl.NumberFormat(lang, { maximumFractionDigits: digits }).format(n);

  // Human name of a locale code in the current language ("fr" -> "French").
  function name(code) {
    try { return new Intl.DisplayNames([lang], { type: 'language' }).of(code); } catch { return code; }
  }

  // Translates a server message {key, vars}.
  const msg = (m) => (m && m.key ? t(m.key, m.vars || {}) : '');

  return { init, t, apply, num, name, msg, get lang() { return lang; }, get available() { return available.slice(); } };
})();
