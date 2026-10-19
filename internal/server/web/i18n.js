// Перевод интерфейса: русский (по умолчанию) и английский.
//
// Ключ — сам русский текст. Так русский режим остаётся ровно прежним
// (t() просто возвращает ключ), а HTML и app.js читаются как раньше.
// Английские тексты лежат в словарях i18n-*.js (по файлу на область:
// панель, мастер, виджет), каждый зовёт I18N.add({...}).
//
// Статичный HTML переводится сам: I18N.apply() обходит текстовые узлы и
// атрибуты (placeholder, title, aria-label, alt) и подменяет те, что есть
// в словаре; оригинал запоминается, чтобы вернуть русский без перезагрузки.
// Пока включён английский, наблюдатель переводит и всё, что дорисовалось
// позже, — так ловятся и динамические надписи без подстановок. Строки с
// подстановками переводятся явно: t("Заказал {who}", {who}).
(function () {
  "use strict";

  const en = Object.create(null);
  let lang = "ru";
  const listeners = [];
  const ATTRS = ["placeholder", "title", "aria-label", "alt", "data-tip"];
  // Оригиналы: узел → русский текст, элемент → {атрибут: русский}.
  const origText = new WeakMap();
  const origAttr = new WeakMap();
  let observer = null;
  let applying = false;

  // Ключи храним и как есть, и со схлопнутыми пробелами: многострочный
  // абзац в HTML (CRLF, отступы) в браузере приходит с другими переводами
  // строк, а переводиться должен одинаково.
  const squeeze = (s) => s.replace(/\s+/g, " ").trim();
  const loose = Object.create(null);
  function add(map) {
    for (const k in map) {
      en[k] = map[k];
      loose[squeeze(k)] = map[k];
    }
  }

  function fill(s, vars) {
    if (!vars) return s;
    return s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));
  }

  // t — перевод с подстановкой {имя}.
  function t(ru, vars) {
    let s = ru;
    if (lang === "en") {
      if (ru in en) s = en[ru];
      else if (squeeze(ru) in loose) s = loose[squeeze(ru)];
    }
    return fill(s, vars);
  }

  // plural(n, ["трек","трека","треков"], ["track","tracks"]) — только слово.
  function plural(n, ru, enForms) {
    const a = Math.abs(n);
    if (lang === "en") return a === 1 ? enForms[0] : enForms[1];
    if (a % 10 === 1 && a % 100 !== 11) return ru[0];
    if (a % 10 >= 2 && a % 10 <= 4 && (a % 100 < 10 || a % 100 >= 20)) return ru[1];
    return ru[2];
  }

  // locale — для toLocaleString / Intl.
  function locale() {
    return lang === "en" ? "en-US" : "ru-RU";
  }

  // Перевод одной строки статичного текста: пробелы по краям сохраняем.
  function translateString(s) {
    const core = s.trim();
    if (!core) return null;
    const out = core in en ? en[core] : loose[squeeze(core)];
    if (out === undefined) return null;
    const i = s.indexOf(core);
    return s.slice(0, i) + out + s.slice(i + core.length);
  }

  function applyNode(node) {
    if (node.nodeType === 3) {
      const p = node.parentNode;
      if (p && (p.nodeName === "SCRIPT" || p.nodeName === "STYLE")) return;
      if (lang === "en") {
        const cur = node.nodeValue;
        const out = translateString(cur);
        if (out !== null && out !== cur) {
          origText.set(node, cur);
          node.nodeValue = out;
        }
      } else if (origText.has(node)) {
        // Возвращаем русский, только если текст не меняли после перевода.
        const orig = origText.get(node);
        if (translateString(orig) === node.nodeValue) node.nodeValue = orig;
        origText.delete(node);
      }
      return;
    }
    if (node.nodeType !== 1) return;
    if (node.nodeName === "SCRIPT" || node.nodeName === "STYLE") return;
    if (node.hasAttribute("data-no-i18n")) return;
    for (const a of ATTRS) {
      if (!node.hasAttribute(a)) continue;
      const saved = origAttr.get(node) || {};
      const cur = node.getAttribute(a);
      if (lang === "en") {
        const out = translateString(cur);
        if (out !== null && out !== cur) {
          saved[a] = cur;
          origAttr.set(node, saved);
          node.setAttribute(a, out);
        }
      } else if (a in saved) {
        if (translateString(saved[a]) === cur) node.setAttribute(a, saved[a]);
        delete saved[a];
      }
    }
    for (let c = node.firstChild; c; c = c.nextSibling) applyNode(c);
  }

  function apply(root) {
    applying = true;
    try {
      applyNode(root || document.documentElement);
      if (!root || root === document.documentElement) {
        document.documentElement.lang = lang;
        if (document.title) {
          if (lang === "en") {
            if (!origText.has(document)) origText.set(document, document.title);
            const out = translateString(origText.get(document));
            if (out !== null) document.title = out;
          } else if (origText.has(document)) {
            document.title = origText.get(document);
          }
        }
      }
    } finally {
      applying = false;
    }
  }

  function watch() {
    if (observer || typeof MutationObserver === "undefined") return;
    observer = new MutationObserver((records) => {
      if (applying || lang !== "en") return;
      applying = true;
      try {
        for (const r of records) {
          if (r.type === "characterData") applyNode(r.target);
          else if (r.type === "attributes") applyNode(r.target);
          else for (const n of r.addedNodes) applyNode(n);
        }
      } finally {
        applying = false;
      }
    });
    observer.observe(document.documentElement, {
      subtree: true, childList: true, characterData: true,
      attributes: true, attributeFilter: ATTRS,
    });
  }

  // setLang переключает язык и перерисовывает статичный текст.
  function setLang(l) {
    l = l === "en" ? "en" : "ru";
    if (l === lang) return false;
    lang = l;
    try { localStorage.setItem("lang", l); } catch (e) { /* без хранилища */ }
    if (document.body) apply();
    for (const fn of listeners) {
      try { fn(l); } catch (e) { console.error(e); }
    }
    return true;
  }

  // До ответа приложения берём язык, выбранный в прошлый раз, — иначе
  // английская панель на миг мигала бы русским.
  try {
    const saved = localStorage.getItem("lang");
    if (saved === "en") lang = "en";
  } catch (e) { /* без хранилища */ }

  function start() {
    apply();
    watch();
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }

  window.I18N = {
    add, t, plural, locale, setLang, apply,
    lang: () => lang,
    onChange: (fn) => listeners.push(fn),
    keys: () => Object.keys(en),
    _en: en,
  };
  window.t = t;
})();
