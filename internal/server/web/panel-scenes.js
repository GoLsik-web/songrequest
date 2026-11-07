// Стиль виджета под сцену OBS .
//
// Под полкой «Мои стили»: каждая сцена OBS — и какой из своих стилей на
// ней надевать. Переключает сам сервер (widget_scenes.go), здесь только
// назначение. Все стили и привязки бесплатны.
(function () {
  "use strict";
  const $ = (id) => document.getElementById(id);
  const t = (ru, v) => (window.I18N ? I18N.t(ru, v) : ru);
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
  const body = $("wx-scenes-body");
  if (!body) return;

  let scenes = null, current = "", error = "", loading = false;

  async function loadScenes() {
    if (loading) return;
    loading = true;
    try {
      const d = await (await fetch("/api/obs/scenes")).json();
      scenes = d.scenes || []; current = d.current || ""; error = d.error || "";
    } catch (e) { error = t("Не получилось связаться с приложением."); }
    loading = false;
    draw();
  }

  function draw() {
    const w = window.SongWidget ? SongWidget.get() : {};
    const styles = w.styles || [], map = w.scene_styles || {};
    if (!styles.length) {
      body.innerHTML = `<div class="hint" style="margin-top:0">${esc(t("Сначала сохрани свои стили выше."))}</div>`;
      return;
    }
    if (scenes === null) { body.innerHTML = `<div class="hint" style="margin-top:0">${esc(t("Смотрю сцены OBS…"))}</div>`; loadScenes(); return; }
    if (error) {
      body.innerHTML = `<div class="hint warn" style="margin-top:0">${esc(error)}</div><button class="act small" data-scenes-retry>${esc(t("Проверить ещё раз"))}</button>`;
      return;
    }
    const opts = (sel, keep) => `<option value="">${esc(keep)}</option>` +
      styles.map((st) => `<option value="${esc(st.id)}"${st.id === sel ? " selected" : ""}>${esc(st.name)}</option>`).join("");
    body.innerHTML = `<div class="scene-styles">` +
      scenes.map((sc) => `<label class="scene-row"><span>${esc(sc)}${sc === current ? ` <i>${esc(t("сейчас в эфире"))}</i>` : ""}</span>
        <select data-scene="${esc(sc)}">${opts(map[sc] || "", t("как на остальных"))}</select></label>`).join("") +
      `<label class="scene-row rest"><span>${esc(t("Остальные сцены"))}</span>
        <select data-scene="">${opts(map[""] || "", t("не менять"))}</select></label></div>
      <button class="act small" data-scenes-retry>${esc(t("Обновить список"))}</button>`;
  }

  body.addEventListener("change", (e) => {
    const sel = e.target.closest("select[data-scene]");
    if (!sel || !window.SongWidget) return;
    const map = Object.assign({}, SongWidget.get().scene_styles || {});
    if (sel.value) map[sel.dataset.scene] = sel.value; else delete map[sel.dataset.scene];
    SongWidget.save({ scene_styles: map });
  });
  body.addEventListener("click", (e) => { if (e.target.closest("[data-scenes-retry]")) { scenes = null; error = ""; draw(); } });

  document.addEventListener("widget-change", draw);
  // раздел «Виджеты» открыли — обновить список сцен
  const panel = $("widget");
  if (panel) new MutationObserver(() => { if (!panel.hidden) { scenes = null; draw(); } }).observe(panel, { attributes: true, attributeFilter: ["hidden"] });
  draw();
})();
