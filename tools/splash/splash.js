// Заставка запуска: улучшенные варианты «Пластинка» и «Сигнал» (25.10).
//
// Каждый вариант — чистая функция «время → кадр»: render(ctx, t, W, H).
// Так одну и ту же анимацию можно и смотреть вживую (preview.html), и
// разрезать на прозрачные PNG-кадры для приложения: фон у кадра пустой,
// видно только сам знак, свечение и окно, в которое знак превращается.
(function () {
  "use strict";

  const LIME = "#c8f751", INK = "#0d1204", BG = "#08090a", LINE = "#1c1f22";
  const T_END = 3.8;           // вся анимация, секунды
  const M0 = 2.6, M1 = 3.35;   // превращение знака в окно

  // Радиус круга на стыке двух фаз бывает «минус ноль с хвостиком»
  // (−1e-14), и браузер на нём бросает ошибку — анимация вставала намертво.
  // Отрицательный радиус для нас всегда значит «круга ещё нет».
  const P2D = CanvasRenderingContext2D.prototype;
  if (!P2D.__safeArc) {
    const arc = P2D.arc;
    P2D.arc = function (x, y, r, a, b, ccw) { return arc.call(this, x, y, Math.max(0, r), a, b, ccw); };
    P2D.__safeArc = true;
  }

  const clamp = (v) => Math.max(0, Math.min(1, v));
  const seg = (t, a, b) => clamp((t - a) / (b - a));
  const lerp = (a, b, p) => a + (b - a) * p;
  const E = {
    inCubic: (p) => p * p * p,
    outCubic: (p) => 1 - Math.pow(1 - p, 3),
    outQuint: (p) => 1 - Math.pow(1 - p, 5),
    inOutCubic: (p) => (p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2),
    outExpo: (p) => (p === 1 ? 1 : 1 - Math.pow(2, -10 * p)),
    outBack: (p, c = 1.7) => 1 + (c + 1) * Math.pow(p - 1, 3) + c * Math.pow(p - 1, 2),
  };

  function rng(seed) {
    return () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
  }

  function rrect(ctx, x, y, w, h, r) {
    r = Math.max(0, Math.min(r, w / 2, h / 2));
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  function mix(a, b, p) {
    const h = (c) => { const n = parseInt(c.slice(1), 16); return [(n >> 16) & 255, (n >> 8) & 255, n & 255]; };
    const pa = h(a), pb = h(b);
    return `rgb(${pa.map((v, i) => Math.round(lerp(v, pb[i], p))).join(",")})`;
  }

  // Окно программы, в которое превращается знак. Совпадает с настоящим
  // окном по месту и цвету — поэтому переход не выглядит слоем поверх.
  function windowRect(W, H) {
    const w = Math.min(W * 0.8, 1180), h = Math.min(H * 0.8, w * 0.68);
    return { x: (W - w) / 2, y: (H - h) / 2, w, h, r: 10 };
  }
  const dotTarget = (win, u) => ({ x: win.x + 18 * u, y: win.y + 17 * u });

  // ── общие эффекты ─────────────────────────────────────────────────

  // «Удар камеры»: всё чуть приближается в момент удара и мягко
  // возвращается, с едва заметной дрожью. Без этого вспышка — просто
  // картинка, с ним — событие.
  function camera(ctx, t, hit, cx, cy, u) {
    const s = t - hit;
    if (s < 0) return;
    const k = Math.exp(-s * 6);
    const z = 1 + 0.05 * k * Math.cos(s * 14);
    const dx = Math.sin(s * 71) * 3 * u * k, dy = Math.cos(s * 53) * 3 * u * k;
    ctx.translate(cx + dx, cy + dy);
    ctx.scale(z, z);
    ctx.translate(-cx, -cy);
  }

  // Вспышка удара: свет из центра, ударная волна и анаморфный блик —
  // горизонтальная полоса, как в кино.
  function flash(ctx, cx, cy, t, t0, R, u) {
    const s = seg(t, t0, t0 + 0.7);
    if (s <= 0 || s >= 1) return;
    ctx.save();
    ctx.globalCompositeOperation = "lighter";
    const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, R * (0.6 + s * 1.6));
    g.addColorStop(0, `rgba(240,255,200,${0.7 * (1 - s)})`);
    g.addColorStop(0.4, `rgba(200,247,81,${0.25 * (1 - s)})`);
    g.addColorStop(1, "rgba(200,247,81,0)");
    ctx.fillStyle = g;
    ctx.fillRect(cx - R * 3, cy - R * 3, R * 6, R * 6);
    const fl = Math.pow(1 - s, 2);
    const len = (260 + s * 520) * u;
    const lg = ctx.createLinearGradient(cx - len, 0, cx + len, 0);
    lg.addColorStop(0, "rgba(200,247,81,0)");
    lg.addColorStop(0.5, `rgba(235,255,190,${0.85 * fl})`);
    lg.addColorStop(1, "rgba(200,247,81,0)");
    ctx.fillStyle = lg;
    ctx.fillRect(cx - len, cy - 1.5 * u, len * 2, 3 * u);
    ctx.globalAlpha = 0.35 * fl;
    ctx.fillRect(cx - len * 0.6, cy - 6 * u, len * 1.2, 12 * u);
    ctx.globalAlpha = 1;
    ctx.globalCompositeOperation = "source-over";
    ctx.strokeStyle = LIME;
    ctx.globalAlpha = (1 - s) * 0.9;
    ctx.lineWidth = 4 * u * (1 - s);
    ctx.shadowColor = LIME;
    ctx.shadowBlur = 12 * u;
    ctx.beginPath();
    ctx.arc(cx, cy, R * (0.8 + E.outQuint(s) * 1.6), 0, Math.PI * 2);
    ctx.stroke();
    ctx.restore();
  }

  // Искры с хвостами: летят от удара, тормозят и гаснут.
  function sparks(ctx, cx, cy, t, t0, R, u, seed, n = 36) {
    const s = seg(t, t0, t0 + 1.1);
    if (s <= 0 || s >= 1) return;
    const rnd = rng(seed);
    ctx.save();
    ctx.globalCompositeOperation = "lighter";
    ctx.lineCap = "round";
    for (let i = 0; i < n; i++) {
      const a = rnd() * Math.PI * 2, sp = 0.5 + rnd() * 1.1, w = (1 + rnd() * 1.8) * u;
      const life = 0.55 + rnd() * 0.45, p = clamp(s / life);
      if (p >= 1) continue;
      const d1 = R * (0.9 + E.outQuint(p) * 1.7 * sp), d0 = R * (0.9 + E.outQuint(Math.max(0, p - 0.08)) * 1.7 * sp);
      ctx.strokeStyle = `rgba(215,255,140,${(1 - p) * 0.9})`;
      ctx.lineWidth = w;
      ctx.beginPath();
      ctx.moveTo(cx + Math.cos(a) * d0, cy + Math.sin(a) * d0);
      ctx.lineTo(cx + Math.cos(a) * d1, cy + Math.sin(a) * d1);
      ctx.stroke();
    }
    ctx.restore();
  }

  // Название: буквы встают из размытия, потом по ним проходит блик.
  //
  // Рисуется на маленьком холсте ровно под текст: размытие по холсту во
  // весь экран на каждой букве в каждом кадре вешало браузер.
  let off = null;
  function word(ctx, cx, y, size, t0, t, fade) {
    if (fade <= 0 || t < t0) return;
    const text = "songrequest";
    const dpr = ctx.getTransform().a || 1;
    off = off || document.createElement("canvas");
    const o = off.getContext("2d");
    o.font = `600 ${size}px Unbounded, sans-serif`;
    const widths = [...text].map((ch) => o.measureText(ch).width);
    const total = widths.reduce((a, b) => a + b, 0);
    const bw = total + size * 2, bh = size * 2.6;
    const pw = Math.ceil(bw * dpr), ph = Math.ceil(bh * dpr);
    if (off.width !== pw || off.height !== ph) { off.width = pw; off.height = ph; }
    o.setTransform(dpr, 0, 0, dpr, 0, 0);
    o.clearRect(0, 0, bw, bh);
    o.font = `600 ${size}px Unbounded, sans-serif`;
    const ox = bw / 2, oy = size * 1.25; // где в маленьком холсте «cx, y»
    let x = ox - total / 2;
    const allIn = t > t0 + text.length * 0.03 + 0.5;
    [...text].forEach((ch, i) => {
      const p = E.outCubic(seg(t, t0 + i * 0.03, t0 + i * 0.03 + 0.5));
      if (p > 0) {
        o.save();
        o.globalAlpha = p;
        o.fillStyle = i < 4 ? "#f2f4f5" : LIME;
        // «размытие» дешёвое: свечение того же цвета, тающее к резкости
        if (!allIn && p < 1) { o.shadowColor = o.fillStyle; o.shadowBlur = (1 - p) * 14; }
        o.translate(x + widths[i] / 2, oy + (1 - p) * size * 0.35);
        o.scale(lerp(1.25, 1, p), lerp(1.25, 1, p));
        o.fillText(ch, -widths[i] / 2, 0);
        o.restore();
      }
      x += widths[i];
    });
    const sp = seg(t, t0 + 0.55, t0 + 1.05);
    if (sp > 0 && sp < 1) {
      o.save();
      o.globalCompositeOperation = "source-atop";
      const bx = ox - total / 2 - size * 2 + (total + size * 4) * E.inOutCubic(sp);
      const g = o.createLinearGradient(bx - size, 0, bx + size, 0);
      g.addColorStop(0, "rgba(255,255,255,0)");
      g.addColorStop(0.5, "rgba(255,255,255,.9)");
      g.addColorStop(1, "rgba(255,255,255,0)");
      o.fillStyle = g;
      o.fillRect(bx - size, 0, size * 2, bh);
      o.restore();
    }
    const sub = seg(t, t0 + 0.6, t0 + 1.0);
    if (sub > 0) {
      o.globalAlpha = sub;
      o.font = `500 ${size * 0.34}px Manrope, sans-serif`;
      o.fillStyle = "#99a1a8";
      o.textAlign = "center";
      o.fillText("заказ музыки для стрима", ox, oy + size * 0.95 + (1 - sub) * 6);
      o.textAlign = "start";
      o.globalAlpha = 1;
    }
    ctx.save();
    ctx.globalAlpha = fade;
    ctx.drawImage(off, cx - ox, y - oy, bw, bh);
    ctx.restore();
  }

  // Набросок панели внутри окна.
  function panel(ctx, win, a, u) {
    if (a <= 0) return;
    ctx.save();
    rrect(ctx, win.x, win.y, win.w, win.h, win.r);
    ctx.clip();
    // Проявляется от центральной линии вверх и вниз — как экран, который
    // включили, а не картинка, которую подложили.
    const open = E.outCubic(a);
    const cy = win.y + win.h / 2;
    ctx.beginPath();
    ctx.rect(win.x, cy - win.h / 2 * open, win.w, win.h * open);
    ctx.clip();
    ctx.globalAlpha = clamp(a * 1.6);
    const { x, y, w } = win;
    const tb = 34 * u;
    ctx.fillStyle = "#0b0c0d";
    ctx.fillRect(x, y, w, tb);
    ctx.fillStyle = LINE;
    ctx.fillRect(x, y + tb, w, 1);
    ctx.fillRect(x, y + tb * 2.2, w, 1);
    ctx.fillStyle = "#f2f4f5";
    ctx.font = `600 ${11 * u}px Unbounded, sans-serif`;
    ctx.textBaseline = "middle";
    ctx.fillText("ЗАКАЗ МУЗЫКИ", x + 32 * u, y + tb / 2);
    const py = y + tb * 2.2 + 36 * u, px = x + 40 * u;
    ctx.fillStyle = LIME;
    ctx.font = `600 ${10 * u}px Manrope, sans-serif`;
    ctx.fillText("● В ЭФИРЕ", px, py);
    ctx.fillStyle = "#15181a";
    ctx.fillRect(px, py + 22 * u, 118 * u, 118 * u);
    ctx.fillStyle = "#f2f4f5";
    ctx.font = `600 ${34 * u}px Unbounded, sans-serif`;
    ctx.fillText("The Only One", px + 142 * u, py + 50 * u);
    ctx.fillStyle = "#99a1a8";
    ctx.font = `500 ${17 * u}px Manrope, sans-serif`;
    ctx.fillText("Chord Overstreet", px + 142 * u, py + 88 * u);
    ctx.fillStyle = LINE;
    ctx.fillRect(px + 142 * u, py + 122 * u, w - 230 * u, 2 * u);
    ctx.fillStyle = LIME;
    ctx.fillRect(px + 142 * u, py + 122 * u, (w - 230 * u) * 0.32, 2 * u);
    ctx.strokeStyle = LINE;
    ctx.strokeRect(x + w * 0.64, py + 190 * u, w * 0.32, 130 * u);
    ctx.restore();
    // полоса света по линии «включения»
    if (a < 1) {
      ctx.save();
      ctx.globalCompositeOperation = "lighter";
      ctx.globalAlpha = (1 - a) * 0.7;
      const lg = ctx.createLinearGradient(win.x, 0, win.x + win.w, 0);
      lg.addColorStop(0, "rgba(200,247,81,0)");
      lg.addColorStop(0.5, "rgba(200,247,81,.8)");
      lg.addColorStop(1, "rgba(200,247,81,0)");
      ctx.fillStyle = lg;
      const h = win.h / 2 * open;
      ctx.fillRect(win.x, cy - h - 1, win.w, 2);
      ctx.fillRect(win.x, cy + h - 1, win.w, 2);
      ctx.restore();
    }
  }

  // Общий финал: фигура знака перетекает в окно, по канту пробегает
  // свет, внутри «включается» панель.
  function morph(ctx, t, W, H, u, from, fillFrom) {
    const p = E.outExpo(seg(t, M0, M1));
    if (p <= 0) return 0;
    const win = windowRect(W, H);
    const x = lerp(from.x, win.x, p), y = lerp(from.y, win.y, p);
    const w = lerp(from.w, win.w, p), h = lerp(from.h, win.h, p), r = lerp(from.r, win.r, p);
    ctx.save();
    ctx.shadowColor = "rgba(0,0,0,.6)";
    ctx.shadowBlur = 50 * u * p;
    ctx.shadowOffsetY = 16 * u * p;
    rrect(ctx, x, y, w, h, r);
    ctx.fillStyle = mix(fillFrom, BG, Math.min(1, p * 1.2));
    ctx.fill();
    ctx.shadowColor = "transparent";
    ctx.lineWidth = 1;
    ctx.strokeStyle = LINE;
    ctx.stroke();
    // свет обегает кант окна и гаснет
    const rp = seg(t, M0 + 0.1, M1 + 0.2);
    if (rp > 0 && rp < 1) {
      const per = 2 * (w + h);
      ctx.setLineDash([per * 0.22, per]);
      ctx.lineDashOffset = -per * E.inOutCubic(rp) * 1.2;
      ctx.lineWidth = 2 * u;
      ctx.strokeStyle = LIME;
      ctx.shadowColor = LIME;
      ctx.shadowBlur = 14 * u;
      ctx.globalAlpha = Math.sin(rp * Math.PI);
      rrect(ctx, x, y, w, h, r);
      ctx.stroke();
    }
    ctx.restore();
    panel(ctx, win, seg(t, M1 - 0.15, T_END - 0.1), u);
    return p;
  }

  // Знак улетает в точку «в эфире» на полосе заголовка.
  function toDot(ctx, t, W, H, u, cx, cy, draw) {
    const p = E.inOutCubic(seg(t, M0 - 0.05, M1 - 0.1));
    const win = windowRect(W, H), d = dotTarget(win, u);
    if (p < 1) {
      ctx.save();
      ctx.translate(lerp(cx, d.x, p), lerp(cy, d.y, p));
      const k = lerp(1, 0.05, E.inCubic(p));
      ctx.scale(k, k);
      ctx.globalAlpha = 1 - seg(p, 0.7, 1);
      draw();
      ctx.restore();
    }
    if (p > 0.8) {
      ctx.save();
      ctx.fillStyle = LIME;
      ctx.shadowColor = LIME;
      ctx.shadowBlur = 10 * u;
      ctx.globalAlpha = seg(p, 0.8, 1);
      ctx.beginPath();
      ctx.arc(d.x, d.y, 3.5 * u, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    }
  }

  // ── 1. Пластинка ──────────────────────────────────────────────────
  //
  // Сюжет: из точки разбегаются дорожки, собирается пластинка, на неё
  // опускается тонарм — иголка касается винила, и в этот момент удар:
  // загорается наклейка со стрелкой «играть», музыка пошла.

  const V_HIT = 1.5;

  function vinyl(ctx, t, W, H) {
    const u = Math.min(W, H) / 700, cx = W / 2 - 14 * u, cy = H / 2 - 40 * u, R = 112 * u;
    const disc = E.outCubic(seg(t, 0.55, 1.05));
    const spin = Math.max(0, t - 0.6);
    const rot = spin < 0.9 ? 2.2 * spin * spin : 2.2 * 0.81 + 4 * (spin - 0.9);

    const record = () => {
      // искра-зерно, с которого всё начинается
      const seed = seg(t, 0, 0.25);
      if (seed > 0 && disc < 1) {
        ctx.save();
        ctx.fillStyle = LIME;
        ctx.shadowColor = LIME;
        ctx.shadowBlur = 20 * u;
        ctx.globalAlpha = 1 - disc;
        ctx.beginPath();
        ctx.arc(0, 0, 4 * u * E.outBack(seed, 3), 0, Math.PI * 2);
        ctx.fill();
        ctx.restore();
      }
      // дорожки разбегаются, как круги по воде
      for (let k = 0; k < 6; k++) {
        const p = E.outQuint(seg(t, 0.08 + k * 0.06, 0.7 + k * 0.06));
        if (p <= 0) continue;
        const rim = k === 5;
        ctx.save();
        ctx.strokeStyle = LIME;
        ctx.globalAlpha = rim ? 1 : (0.9 - k * 0.12) * (1 - disc);
        ctx.lineWidth = (rim ? 3 : 1.6) * u;
        if (rim) { ctx.shadowColor = LIME; ctx.shadowBlur = 24 * u; }
        ctx.beginPath();
        ctx.arc(0, 0, R * (0.3 + 0.14 * k) * p, 0, Math.PI * 2);
        ctx.stroke();
        ctx.restore();
      }
      if (disc > 0) {
        ctx.save();
        // винил проявляется из центра наружу
        ctx.beginPath();
        ctx.arc(0, 0, (R - 1.5 * u) * disc, 0, Math.PI * 2);
        ctx.clip();
        const g = ctx.createRadialGradient(0, 0, R * 0.2, 0, 0, R);
        g.addColorStop(0, "#1c2016");
        g.addColorStop(0.7, "#0b0c09");
        g.addColorStop(1, "#050604");
        ctx.fillStyle = g;
        ctx.fillRect(-R, -R, 2 * R, 2 * R);
        ctx.rotate(rot);
        // канавки: тонкие, чуть разной яркости
        for (let i = 0; i < 26; i++) {
          ctx.strokeStyle = `rgba(255,255,255,${0.025 + (i % 3) * 0.012})`;
          ctx.lineWidth = 0.8;
          ctx.beginPath();
          ctx.arc(0, 0, R * (0.38 + i * 0.023), 0, Math.PI * 2);
          ctx.stroke();
        }
        // радужный отлив винила — два вращающихся блика
        const cg = ctx.createConicGradient(0, 0, 0);
        [[0, 0], [0.05, 0.2], [0.09, 0.05], [0.14, 0], [0.5, 0], [0.55, 0.14], [0.6, 0], [1, 0]].forEach(([s, a]) =>
          cg.addColorStop(s, `rgba(220,255,170,${a})`));
        ctx.fillStyle = cg;
        ctx.fillRect(-R, -R, 2 * R, 2 * R);
        const ig = ctx.createConicGradient(1.2, 0, 0);
        [[0, "rgba(90,220,210,0)"], [0.08, "rgba(90,220,210,.07)"], [0.16, "rgba(170,120,255,.06)"], [0.24, "rgba(170,120,255,0)"], [1, "rgba(0,0,0,0)"]]
          .forEach(([s, c]) => ig.addColorStop(s, c));
        ctx.fillStyle = ig;
        ctx.fillRect(-R, -R, 2 * R, 2 * R);
        ctx.restore();

        // наклейка: тёмная до удара, лаймовая после
        const lit = E.outBack(seg(t, V_HIT, V_HIT + 0.35), 2.5);
        const lr = R * 0.32 * E.outBack(seg(t, 0.8, 1.15));
        ctx.save();
        ctx.fillStyle = mix("#1e2414", LIME, clamp(lit));
        if (lit > 0) { ctx.shadowColor = LIME; ctx.shadowBlur = 26 * u * clamp(lit); }
        ctx.beginPath();
        ctx.arc(0, 0, lr * (1 + 0.08 * Math.sin(clamp(lit) * Math.PI)), 0, Math.PI * 2);
        ctx.fill();
        ctx.shadowBlur = 0;
        ctx.fillStyle = "#050604";
        ctx.beginPath();
        ctx.arc(0, 0, 2.5 * u, 0, Math.PI * 2);
        ctx.fill();
        ctx.restore();
        // стрелка «играть» проступает на наклейке
        if (lit > 0) {
          const s = R * 0.14 * clamp(lit);
          ctx.save();
          ctx.translate(s * 0.18, 0);
          ctx.fillStyle = INK;
          ctx.strokeStyle = INK;
          ctx.lineJoin = "round";
          ctx.lineWidth = 4 * u;
          ctx.beginPath();
          ctx.moveTo(-s * 0.7, -s);
          ctx.lineTo(s, 0);
          ctx.lineTo(-s * 0.7, s);
          ctx.closePath();
          ctx.fill();
          ctx.stroke();
          ctx.restore();
        }
      }
    };

    // тонарм: выезжает, опускает иглу, после — уходит
    const arm = () => {
      const pin = seg(t, 0.95, V_HIT - 0.05), pout = seg(t, 2.2, 2.55);
      if (pin <= 0 || pout >= 1) return;
      const px = R * 1.22, py = -R * 1.02;
      const play = Math.atan2(R * 0.42 - py, R * 0.5 - px);
      const rest = play - 0.75;
      const a = lerp(lerp(rest, play, E.outBack(pin, 1.2)), rest - 0.2, E.inOutCubic(pout));
      const L = Math.hypot(R * 0.5 - px, R * 0.42 - py);
      const lift = 1 - seg(t, V_HIT - 0.12, V_HIT); // игла «падает» в канавку
      ctx.save();
      ctx.globalAlpha = clamp(pin * 3) * (1 - pout);
      ctx.translate(px, py);
      // тень руки на виниле
      ctx.save();
      ctx.rotate(a);
      ctx.translate(6 * u + lift * 8 * u, 8 * u + lift * 8 * u);
      ctx.strokeStyle = "rgba(0,0,0,.45)";
      ctx.lineWidth = 7 * u;
      ctx.lineCap = "round";
      ctx.beginPath(); ctx.moveTo(0, 0); ctx.lineTo(L, 0); ctx.stroke();
      ctx.restore();
      ctx.rotate(a);
      const tube = ctx.createLinearGradient(0, -3 * u, 0, 3 * u);
      tube.addColorStop(0, "#f4f6f7");
      tube.addColorStop(1, "#7d848a");
      ctx.strokeStyle = tube;
      ctx.lineWidth = 5 * u;
      ctx.lineCap = "round";
      ctx.beginPath(); ctx.moveTo(0, 0); ctx.lineTo(L - 14 * u, 0); ctx.stroke();
      // головка с иглой
      ctx.translate(L - 14 * u, 0);
      ctx.rotate(0.35);
      ctx.fillStyle = "#d9dee2";
      rrect(ctx, -2 * u, -6 * u, 24 * u, 12 * u, 3 * u);
      ctx.fill();
      ctx.fillStyle = LIME;
      ctx.shadowColor = LIME;
      ctx.shadowBlur = 10 * u;
      ctx.beginPath(); ctx.arc(18 * u, 0, 2.6 * u, 0, Math.PI * 2); ctx.fill();
      ctx.restore();
      // ось тонарма
      ctx.save();
      ctx.globalAlpha = clamp(pin * 3) * (1 - pout);
      ctx.translate(px, py);
      ctx.fillStyle = "#15181a";
      ctx.strokeStyle = "#3b4147";
      ctx.lineWidth = 2 * u;
      ctx.beginPath(); ctx.arc(0, 0, 13 * u, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
      ctx.strokeStyle = LIME;
      ctx.beginPath(); ctx.arc(0, 0, 6 * u, 0, Math.PI * 2); ctx.stroke();
      ctx.restore();
    };

    // волны звука с пластинки — в такт, после удара
    const beats = () => {
      for (let k = 0; k < 3; k++) {
        const s = seg(t, V_HIT + 0.25 + k * 0.42, V_HIT + 1.05 + k * 0.42);
        if (s <= 0 || s >= 1) continue;
        ctx.save();
        ctx.strokeStyle = LIME;
        ctx.globalAlpha = (1 - s) * 0.45 * (1 - seg(t, 2.4, M0));
        ctx.lineWidth = 1.5 * u;
        ctx.beginPath();
        ctx.arc(0, 0, R * (1.05 + E.outCubic(s) * 0.9), 0, Math.PI * 2);
        ctx.stroke();
        ctx.restore();
      }
    };

    const mark = () => { beats(); record(); arm(); };

    ctx.save();
    camera(ctx, t, V_HIT, cx, cy, u);
    if (t < M0 - 0.05) { ctx.save(); ctx.translate(cx, cy); mark(); ctx.restore(); }
    flash(ctx, cx, cy, t, V_HIT, R, u);
    sparks(ctx, cx, cy, t, V_HIT, R, u, 11);
    ctx.restore();
    word(ctx, cx + 14 * u, cy + R + 76 * u, 42 * u, 1.7, t, 1 - seg(t, 2.35, 2.65));
    const p = morph(ctx, t, W, H, u, { x: cx - R, y: cy - R, w: 2 * R, h: 2 * R, r: R }, "#0b0c09");
    if (t >= M0 - 0.05) toDot(ctx, t, W, H, u, cx, cy, () => { record(); });
    return p;
  }

  // ── 2. Сигнал ─────────────────────────────────────────────────────
  //
  // Сюжет: звуковая волна бежит слева к центру, на месте сжимается в один
  // острый пик — как удар в записи, — пик схлопывается в точку, точка
  // вспыхивает и начинает «вещать» дугами в такт.

  const S_HIT = 1.42;

  function signal(ctx, t, W, H) {
    const u = Math.min(W, H) / 700, cx = W / 2, cy = H / 2 - 40 * u;
    const run = seg(t, 0, 1.05);
    const squeeze = E.inOutCubic(seg(t, 0.95, 1.3));
    const drop = E.inCubic(seg(t, 1.27, S_HIT));

    // Траектория полёта: влетает из-за левого нижнего края, делает дугу и
    // выходит к центру горизонтально. Кубическая кривая Безье.
    const P0 = [-560 * u, 210 * u], P1 = [-330 * u, -260 * u], P2 = [-170 * u, 90 * u], P3 = [0, 0];
    const bez = (q) => {
      const a = 1 - q;
      return [
        a * a * a * P0[0] + 3 * a * a * q * P1[0] + 3 * a * q * q * P2[0] + q * q * q * P3[0],
        a * a * a * P0[1] + 3 * a * a * q * P1[1] + 3 * a * q * q * P2[1] + q * q * q * P3[1],
      ];
    };
    const tangent = (q) => {
      const a = bez(Math.max(0, q - 0.002)), b = bez(Math.min(1, q + 0.002));
      const dx = b[0] - a[0], dy = b[1] - a[1], l = Math.hypot(dx, dy) || 1;
      return [dx / l, dy / l];
    };

    // Голова летит быстро и плавно тормозит у центра; хвост догоняет.
    const qHead = E.outQuint(run);
    const qTail = Math.max(0, qHead - lerp(0.62, 0.0, E.inCubic(seg(t, 0.75, 1.3))));
    // «Удары баса»: вздутия, которые катятся по волне от хвоста к голове.
    const kicks = [0.18, 0.46, 0.7, 0.88];

    const wave = () => {
      if (drop >= 1 || run <= 0) return;
      const N = 170;
      const pts = [];
      for (let i = 0; i <= N; i++) {
        const k = i / N;                       // 0 — хвост, 1 — голова
        const q = lerp(qTail, qHead, k);
        let [x, y] = bez(q);
        // сжатие в пик: вся лента стягивается к центру по горизонтали
        x = lerp(x, x * 0.015, squeeze);
        y = lerp(y, y * 0.015, squeeze);
        const [tx, ty] = tangent(q);
        // огибающая: хвост тает в ноль, у головы волна сужается в иглу
        let env = Math.pow(Math.sin(k * Math.PI * 0.96), 1.2) * Math.pow(k, 0.7);
        let bump = 0;
        kicks.forEach((k0) => {
          const pos = (t - k0) * 2.4;          // где сейчас вздутие (0…1 по ленте)
          if (pos > -0.2 && pos < 1.2) bump += Math.exp(-Math.pow((k - pos) / 0.07, 2)) * (1 - pos * 0.5);
        });
        // при сжатии колебания встают строго вертикально — пик, а не косая черта
        let nx = lerp(-ty, 0, squeeze), ny = lerp(tx, 1, squeeze);
        const nl = Math.hypot(nx, ny) || 1;
        pts.push({ x, y, nx: nx / nl, ny: ny / nl, k, env, bump });
      }

      // Три нити, закрученные лентой: фаза бежит по волне, а не едет с ней.
      const amp = 36 * u * (1 + squeeze * 2.8) * (1 - drop);
      const twist = 16 + squeeze * 30;
      const speed = 10 + squeeze * 26;
      for (let strand = 0; strand < 3; strand++) {
        const ph = (strand * Math.PI * 2) / 3;
        let prev = null;
        ctx.save();
        ctx.lineCap = "round";
        pts.forEach((p) => {
          const a = p.k * twist - t * speed + ph;
          const swirl = Math.sin(a) * (1 + p.bump * 1.6);
          const depth = Math.cos(a);           // −1 — нить сзади, 1 — спереди
          const d = swirl * amp * p.env;
          const x = p.x + p.nx * d, y = p.y + p.ny * d;
          if (prev) {
            const front = 0.5 + 0.5 * depth;
            ctx.strokeStyle = mix("#3d4a1c", "#e6ffb0", front * (0.5 + 0.5 * Math.min(1, p.env * 1.4 + p.bump)));
            ctx.globalAlpha = (0.15 + 0.85 * front) * Math.min(1, p.env * 2.2 + 0.05) * (1 - drop * 0.4);
            ctx.lineWidth = (0.8 + 3.2 * front + p.bump * 2.2) * u * (0.35 + 0.65 * p.env);
            if (front > 0.8 && p.k > 0.35) { ctx.shadowColor = LIME; ctx.shadowBlur = 10 * u; } else ctx.shadowBlur = 0;
            ctx.beginPath();
            ctx.moveTo(prev[0], prev[1]);
            ctx.lineTo(x, y);
            ctx.stroke();
          }
          prev = [x, y];
        });
        ctx.restore();
      }

      // Искры, которые срываются с гребней на «ударах».
      const rnd = rng(17);
      ctx.save();
      ctx.globalCompositeOperation = "lighter";
      for (let i = 0; i < 46; i++) {
        const born = 0.12 + rnd() * 0.85, life = 0.35 + rnd() * 0.35;
        const age = (t - born) / life;
        const kk = 0.35 + rnd() * 0.6;
        if (age <= 0 || age >= 1 || squeeze > 0.6) continue;
        const p = pts[Math.round(kk * N)];
        const side = rnd() > 0.5 ? 1 : -1, drift = (14 + rnd() * 40) * u * E.outCubic(age);
        const x = p.x + p.nx * side * (amp * p.env + drift) - (tangent(lerp(qTail, qHead, kk))[0]) * drift * 0.8;
        const y = p.y + p.ny * side * (amp * p.env + drift) + 10 * u * age * age;
        ctx.globalAlpha = (1 - age) * 0.9;
        ctx.fillStyle = "#dfff9a";
        ctx.beginPath();
        ctx.arc(x, y, (0.8 + rnd() * 1.6) * u * (1 - age * 0.5), 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.restore();

      // Голова: яркая точка со шлейфом вдоль полёта.
      if (squeeze < 1) {
        const h = pts[N], [tx, ty] = tangent(qHead);
        const vel = 1 - qHead;                 // чем быстрее летит, тем длиннее шлейф
        ctx.save();
        ctx.globalCompositeOperation = "lighter";
        const len = (30 + vel * 140) * u;
        const lg = ctx.createLinearGradient(h.x, h.y, h.x - tx * len, h.y - ty * len);
        lg.addColorStop(0, `rgba(240,255,200,${0.95 * (1 - squeeze)})`);
        lg.addColorStop(1, "rgba(200,247,81,0)");
        ctx.strokeStyle = lg;
        ctx.lineWidth = 5 * u;
        ctx.lineCap = "round";
        ctx.beginPath();
        ctx.moveTo(h.x, h.y);
        ctx.lineTo(h.x - tx * len, h.y - ty * len);
        ctx.stroke();
        const g = ctx.createRadialGradient(h.x, h.y, 0, h.x, h.y, 30 * u);
        g.addColorStop(0, `rgba(250,255,225,${1 - squeeze})`);
        g.addColorStop(0.25, `rgba(200,247,81,${0.6 * (1 - squeeze)})`);
        g.addColorStop(1, "rgba(200,247,81,0)");
        ctx.fillStyle = g;
        ctx.fillRect(h.x - 30 * u, h.y - 30 * u, 60 * u, 60 * u);
        ctx.restore();
      }
    };

    const dot = () => {
      const dp = E.outBack(seg(t, S_HIT, S_HIT + 0.35), 3);
      if (dp <= 0) return;
      const breathe = 1 + 0.06 * Math.sin((t - S_HIT) * Math.PI * 4);
      ctx.save();
      ctx.fillStyle = LIME;
      ctx.shadowColor = LIME;
      ctx.shadowBlur = 34 * u;
      ctx.beginPath();
      ctx.arc(0, 0, 19 * u * dp * breathe, 0, Math.PI * 2);
      ctx.fill();
      ctx.shadowBlur = 0;
      ctx.fillStyle = "rgba(255,255,255,.55)";
      ctx.beginPath();
      ctx.arc(-5 * u, -5 * u, 5 * u * dp, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    };

    const arcs = () => {
      const ap = seg(t, S_HIT + 0.08, S_HIT + 0.6);
      if (ap <= 0) return;
      ctx.save();
      ctx.lineCap = "round";
      for (let k = 0; k < 3; k++) {
        const grow = E.outBack(clamp(ap * 1.5 - k * 0.22), 2);
        if (grow <= 0) continue;
        // такт: каждая дуга загорается чуть позже соседки — «бежит» наружу
        const beat = Math.pow(0.5 + 0.5 * Math.cos((t - S_HIT) * Math.PI * 4 - k * 1.1), 2);
        const r = (46 + k * 28) * u * grow;
        ctx.strokeStyle = LIME;
        ctx.shadowColor = LIME;
        ctx.shadowBlur = 14 * u * beat;
        ctx.globalAlpha = 0.3 + 0.7 * beat;
        ctx.lineWidth = (4.5 - k * 0.8) * u;
        [[-0.72, 0.72], [Math.PI - 0.72, Math.PI + 0.72]].forEach(([a, b]) => {
          ctx.beginPath(); ctx.arc(0, 0, r, a, b); ctx.stroke();
        });
      }
      ctx.restore();
      // на каждый такт по кругу уходит тонкое кольцо — «сигнал в эфир»
      for (let k = 0; k < 3; k++) {
        const s = seg(t, S_HIT + 0.3 + k * 0.5, S_HIT + 1.2 + k * 0.5);
        if (s <= 0 || s >= 1) continue;
        ctx.save();
        ctx.strokeStyle = LIME;
        ctx.globalAlpha = (1 - s) * 0.35 * (1 - seg(t, 2.4, M0));
        ctx.lineWidth = 1.2 * u;
        ctx.beginPath();
        ctx.arc(0, 0, (110 + E.outCubic(s) * 140) * u, 0, Math.PI * 2);
        ctx.stroke();
        ctx.restore();
      }
    };

    const mark = () => { wave(); arcs(); dot(); };

    ctx.save();
    camera(ctx, t, S_HIT, cx, cy, u);
    if (t < M0 - 0.05) { ctx.save(); ctx.translate(cx, cy); mark(); ctx.restore(); }
    flash(ctx, cx, cy, t, S_HIT, 70 * u, u);
    sparks(ctx, cx, cy, t, S_HIT, 26 * u, u, 5, 30);
    ctx.restore();
    word(ctx, cx, cy + 160 * u, 42 * u, 1.68, t, 1 - seg(t, 2.35, 2.65));
    const R = 104 * u;
    const p = morph(ctx, t, W, H, u, { x: cx - R, y: cy - R, w: 2 * R, h: 2 * R, r: R }, "#0a0b08");
    if (t >= M0 - 0.05) toDot(ctx, t, W, H, u, cx, cy, () => { arcs(); dot(); });
    return p;
  }

  // Звук: тёплый аккорд, на ударе низкий «бум» и искорка, на раскрытии
  // окна — выдох. Берёт любой звуковой контекст, и живой, и «в файл».
  function sound(c, hit) {
    const t0 = c.currentTime;
    const master = c.createGain(); master.gain.value = 0.18; master.connect(c.destination);
    const lp = c.createBiquadFilter(); lp.type = "lowpass";
    lp.frequency.setValueAtTime(500, t0); lp.frequency.exponentialRampToValueAtTime(3500, t0 + hit);
    lp.connect(master);
    const note = (f, at, len, type, g, atk = 0.02, out = lp) => {
      const o = c.createOscillator(), v = c.createGain(), t = t0 + at;
      o.type = type; o.frequency.value = f;
      v.gain.setValueAtTime(0.0001, t);
      v.gain.exponentialRampToValueAtTime(g, t + atk);
      v.gain.exponentialRampToValueAtTime(0.0001, t + len);
      o.connect(v).connect(out); o.start(t); o.stop(t + len + 0.05);
    };
    [174.6, 261.6, 329.6, 392.0, 440.0].forEach((f, i) => note(f, 0.05 + i * 0.05, hit + 1.4, i ? "sine" : "triangle", i ? 0.2 : 0.28, 0.7));
    note(65, hit, 0.6, "sine", 0.9, 0.005, master);
    [1046.5, 1318.5, 1568.0, 2093.0, 2637].forEach((f, i) => note(f, hit + 0.02 + i * 0.06, 0.55, "sine", 0.1));
    const len = 0.7, buf = c.createBuffer(1, c.sampleRate * len, c.sampleRate), d = buf.getChannelData(0);
    const rnd = rng(99);
    for (let i = 0; i < d.length; i++) d[i] = (rnd() * 2 - 1) * (1 - i / d.length);
    const src = c.createBufferSource(), bp = c.createBiquadFilter(), g = c.createGain(), tw = t0 + M0;
    src.buffer = buf; bp.type = "bandpass"; bp.Q.value = 0.8;
    bp.frequency.setValueAtTime(300, tw); bp.frequency.exponentialRampToValueAtTime(5000, tw + len);
    g.gain.setValueAtTime(0.0001, tw); g.gain.exponentialRampToValueAtTime(0.45, tw + 0.15);
    g.gain.exponentialRampToValueAtTime(0.0001, tw + len);
    src.connect(bp).connect(g).connect(master); src.start(tw);
  }

  window.Splash = {
    T_END, M0, sound,
    variants: [
      { id: "vinyl", name: "Пластинка", render: vinyl, hit: V_HIT },
      { id: "signal", name: "Сигнал", render: signal, hit: S_HIT },
    ],
  };
})();
