// Проверка словарей: у каждого русского текста интерфейса есть английский.
// Запуск: node i18ncheck.js <папка web>. Выход 1 и список — если чего-то нет.
"use strict";
const fs = require("fs");
const path = require("path");
const vm = require("vm");
const dir = process.argv[2];

// Загружаем i18n.js и словари в песочнице без браузера.
const ctx = { console, localStorage: { getItem() { return null; }, setItem() {} },
  document: { readyState: "complete", documentElement: {}, addEventListener() {} } };
ctx.window = ctx;
vm.createContext(ctx);
const run = (f) => vm.runInContext(fs.readFileSync(path.join(dir, f), "utf8"), ctx, { filename: f });
// apply() без DOM падать не должен: подменяем на время загрузки.
ctx.document.documentElement = { nodeType: 9 };
run("i18n.js");
const dicts = fs.readdirSync(dir).filter((f) => /^i18n-.*\.js$/.test(f));
for (const f of dicts) run(f);
const en = ctx.I18N._en;
const sq = (x) => x.replace(/\s+/g, " ").trim();
const looseKeys = new Set(Object.keys(en).map(sq));
const has = (x) => x in en || looseKeys.has(sq(x));

const cyr = /[А-Яа-яЁё]/;
const missing = [];
const bad = [];
for (const [k, v] of Object.entries(en)) {
  if (cyr.test(v)) bad.push(`кириллица в переводе: ${JSON.stringify(k)} → ${JSON.stringify(v)}`);
  const ph = (s) => (s.match(/\{\w+\}/g) || []).sort().join();
  if (ph(k) !== ph(v)) bad.push(`разные {подстановки}: ${JSON.stringify(k)} → ${JSON.stringify(v)}`);
}

// 1) Все t("…") / I18N.t("…") с русским текстом в JS и HTML.
const files = fs.readdirSync(dir).filter((f) => /\.(js|html)$/.test(f) && !/^i18n/.test(f));
const lit = /(?:^|[^\w.$])(?:I18N\.)?t\(\s*(["'`])((?:\.|(?!\1).)*)\1/g;
for (const f of files) {
  const src = fs.readFileSync(path.join(dir, f), "utf8");
  for (const m of src.matchAll(lit)) {
    let s = m[2];
    if (m[1] === "`" && s.includes("${")) { bad.push(`${f}: шаблон с \${} в t(): ${s}`); continue; }
    s = s.replace(/\\(.)/g, (m0, c) => (c === "n" ? "\n" : c));
    if (cyr.test(s) && !has(s)) missing.push(`${f}: t(${JSON.stringify(s)})`);
  }
}

// 2) Русский статичный текст и атрибуты в HTML.
for (const f of files.filter((f) => f.endsWith(".html"))) {
  let html = fs.readFileSync(path.join(dir, f), "utf8")
    .replace(/<!--[\s\S]*?-->/g, " ")
    .replace(/<script[\s\S]*?<\/script>/gi, " ")
    .replace(/<style[\s\S]*?<\/style>/gi, " ");
  const decode = (s) => s.replace(/&nbsp;/g, " ").replace(/&laquo;/g, "«").replace(/&raquo;/g, "»")
    .replace(/&mdash;/g, "—").replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"');
  for (const m of html.matchAll(/\s(?:placeholder|title|aria-label|alt|data-tip)="([^"]*)"/g)) {
    const s = decode(m[1]).trim();
    if (cyr.test(s) && !has(s)) missing.push(`${f}: атрибут ${JSON.stringify(s)}`);
  }
  html = html.replace(/<[^>]*>/g, "\u0000");
  for (const part of html.split("\u0000")) {
    const s = decode(part).trim();
    if (s && cyr.test(s) && !has(s)) missing.push(`${f}: текст ${JSON.stringify(s)}`);
  }
}

for (const b of bad) console.log(b);
for (const m of missing) console.log("нет перевода — " + m);
console.log(`ключей в словарях: ${Object.keys(en).length}`);
process.exit(missing.length || bad.length ? 1 : 0);
