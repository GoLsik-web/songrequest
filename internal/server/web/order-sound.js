// Звук нового заказа. Общий для виджета (там он звучит на стриме) и для
// панели (там его слушают, выбирая).
//
// Встроенные звуки не файлы, а синтез: короткие, без авторских прав, и
// весят ноль байт. Свой звук — файл, загруженный в панели.
(function () {
  "use strict";

  let ctx = null;
  function audio() {
    if (!ctx) ctx = new (window.AudioContext || window.webkitAudioContext)();
    // Браузер панели будит звук только после нажатия, OBS — сразу.
    if (ctx.state === "suspended") ctx.resume();
    return ctx;
  }

  // tone — одна нота: частота, начало, длина, форма волны, громкость и
  // скольжение частоты к концу (для «пузыря» и «винила»).
  function tone(out, f, at, len, type = "sine", gain = 1, slideTo = 0) {
    const c = out.context;
    const osc = c.createOscillator();
    const g = c.createGain();
    const t = c.currentTime + at;
    osc.type = type;
    osc.frequency.setValueAtTime(f, t);
    if (slideTo) osc.frequency.exponentialRampToValueAtTime(slideTo, t + len);
    g.gain.setValueAtTime(0.0001, t);
    g.gain.exponentialRampToValueAtTime(gain, t + 0.008);
    g.gain.exponentialRampToValueAtTime(0.0001, t + len);
    osc.connect(g).connect(out);
    osc.start(t);
    osc.stop(t + len + 0.02);
  }

  const SOUNDS = {
    ding:    (o) => { tone(o, 1318, 0, 0.9, "sine", 0.8); tone(o, 2637, 0, 0.5, "sine", 0.15); },
    pop:     (o) => { tone(o, 420, 0, 0.12, "sine", 1, 900); },
    chime:   (o) => { tone(o, 784, 0, 0.6, "sine", 0.6); tone(o, 988, 0.12, 0.6, "sine", 0.6); tone(o, 1175, 0.24, 0.9, "sine", 0.6); },
    coin:    (o) => { tone(o, 988, 0, 0.08, "square", 0.25); tone(o, 1319, 0.08, 0.4, "square", 0.25); },
    bubble:  (o) => { tone(o, 300, 0, 0.18, "sine", 0.9, 1200); tone(o, 500, 0.12, 0.15, "sine", 0.6, 1600); },
    levelup: (o) => { [523, 659, 784, 1047].forEach((f, i) => tone(o, f, i * 0.07, 0.22, "triangle", 0.5)); },
    vinyl:   (o) => { tone(o, 1600, 0, 0.35, "sawtooth", 0.12, 200); tone(o, 660, 0.3, 0.5, "sine", 0.6); },
    soft:    (o) => { tone(o, 660, 0, 0.7, "triangle", 0.35); tone(o, 880, 0.1, 0.8, "triangle", 0.25); },
  };

  // play проигрывает звук по настройкам. vol — 1…100.
  function play(cfg) {
    if (!cfg) return;
    const vol = Math.max(1, Math.min(100, cfg.volume || 60)) / 100;
    if (cfg.sound === "file" && cfg.file) {
      const a = new Audio("/media/" + cfg.file);
      a.volume = vol;
      a.play().catch(() => {});
      return;
    }
    const make = SOUNDS[cfg.sound] || SOUNDS.ding;
    try {
      const c = audio();
      const out = c.createGain();
      out.gain.value = vol;
      out.connect(c.destination);
      make(out);
    } catch { /* без звука виджет всё равно работает */ }
  }

  window.OrderSound = { play, names: Object.keys(SOUNDS) };
})();
