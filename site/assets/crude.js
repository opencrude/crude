/* Crude — the website as a living Crude shell.
   One fixed "screen" morphs from the welcome screen into the workspace; scroll progress scrubs every scene.
   No dependencies. Every visual state is a pure function of scroll position, so it is reversible and deterministic. */
(() => {
  "use strict";
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
  const clamp = (v, a = 0, b = 1) => Math.min(b, Math.max(a, v));
  const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);
  const lerp = (a, b, t) => a + (b - a) * t;
  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ───────────── Install: every system listed, the visitor's own one lit ───────────── */
  function detectOS() {
    const ua = navigator.userAgent || "";
    const p = ((navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "").toLowerCase();
    if (p.includes("win") || /Windows/i.test(ua)) return "windows";
    if (/android/i.test(ua)) return "linux";
    if (/iphone|ipad|ipod/i.test(ua) || p.includes("mac") || /Mac OS X/i.test(ua)) return "macos";
    return "linux";
  }
  const detected = detectOS();
  $$(`.ins-row[data-os="${detected}"]`).forEach((row) => row.classList.add("is-detected"));
  // The whole row copies, not only its button. A drag that selects text is left alone.
  $$(".ins-row").forEach((row) => {
    const b = $("[data-copy]", row);
    if (!b) return;
    row.classList.add("is-copyable");
    row.addEventListener("click", (e) => {
      if (e.target.closest("a")) return;
      const sel = window.getSelection();
      if (!b.contains(e.target) && sel && !sel.isCollapsed && row.contains(sel.anchorNode)) return;
      copyCommand(b);
    });
  });

  async function copyText(text) {
    try { await navigator.clipboard.writeText(text); return true; } catch (_) { /* fall through */ }
    const area = document.createElement("textarea");
    area.value = text; area.setAttribute("readonly", ""); area.style.position = "fixed"; area.style.opacity = "0";
    document.body.appendChild(area); area.select();
    let ok = false; try { ok = document.execCommand("copy"); } catch (_) { ok = false; }
    area.remove(); return ok;
  }
  async function copyCommand(button) {
    const ok = await copyText(button.dataset.copy);
    button.textContent = ok ? "[ Copied ✓ ]" : "[ Select and copy ]";
    button.classList.toggle("is-done", ok);
    clearTimeout(button._t);
    button._t = setTimeout(() => { button.textContent = "[ Copy ]"; button.classList.remove("is-done"); }, 1800);
  }

  /* ───────────── Scroll engine ───────────── */
  const screen = $("#screen");
  const boot = $("#boot");
  const banner = $(".hero .banner");
  const brand = $(".w-brand");
  const more = $("#more");
  const chapters = $$(".chapter");
  const scenes = {};
  $$(".scene").forEach((s) => (scenes[s.dataset.scene] = s));
  const ORDER = ["chat", "agents", "memory", "lsp", "core", "review", "errand"];
  const STEPS = { memory: [0.62], lsp: [0.14, 0.4, 0.62, 0.8], review: [0.3, 0.5, 0.8], errand: [0.45, 0.72] };
  const RAIL = { chat: 0, agents: 1, memory: 4, lsp: 0, core: 0, review: 0, errand: 1 };
  const CORE_OFF = [0.34, 0.46, 0.6, 0.7]; // the window closes, stays closed, then opens again
  const CAPTION = 0.54;                    // crude.css shows .boot-caption fully for --boot .44–.64
  const REVEAL = 1 / 14;                   // crude.css: an .r element appears over this much progress
  const hero = $(".hero");
  const rail = $$(".ws-rail .ri");
  const typed = $$(".typed").map((el) => ({
    el, scene: el.closest(".scene").dataset.scene, text: el.dataset.text,
    from: +el.dataset.from, to: +el.dataset.to, clear: +el.dataset.clear, comp: el.closest(".composer"), n: -1,
  }));

  let vw = 0, vh = 0, mobile = false, land = false, heroR, chapR, bootTop = 0, bootLen = 1, chapGeo = [], moreTop = 0, moreEnd = 0, stops = [];
  let bannerBase = null, raf = 0, lastScene = "", lastBp = -1, lastNav = null;
  const navLinks = $$('.nav-links a[href^="#"]');

  function layout() {
    vw = document.documentElement.clientWidth; vh = window.innerHeight; mobile = vw < 900;
    land = mobile && vw > vh && vh < 560;
    document.documentElement.classList.toggle("land", land);
    const fx = mobile ? 8 : 16, fy = mobile ? 56 : 60, fb = mobile ? 8 : 16;
    heroR = { x: fx, y: fy, w: vw - 2 * fx, h: vh - fy - fb, r: mobile ? 10 : 14 };
    if (mobile) {
      chapR = land ? { x: 8, y: 56, w: Math.round(vw * 0.56) - 12, h: vh - 64, r: 10 } : { x: 8, y: 56, w: vw - 16, h: Math.round(Math.min(vh * 0.5, 520)), r: 10 };
    } else {
      const colX = clamp(vw * 0.055, 24, 104);
      const colW = vw <= 1100 ? Math.min(440, vw * 0.38) : Math.min(500, vw * 0.35);
      const x = Math.round(colX + colW + Math.max(36, vw * 0.035));
      const right = Math.round(clamp(vw * 0.025, 16, 40));
      chapR = { x, y: 76, w: vw - x - right, h: vh - 76 - 28, r: 12 };
    }
    const oilEl = $("#oil");
    oilEl.style.width = Math.round(mobile ? heroR.w : heroR.w * 0.52) + "px";
    oilEl.style.height = Math.round(mobile ? heroR.h * 0.42 : heroR.h) + "px";
    oilEl.style.display = "";
    if (mobile) {
      const hero = $(".hero"), ins = $("#install");
      const free = heroR.y + heroR.h - (hero.offsetTop + ins.offsetTop + ins.offsetHeight) - 12;
      const h = Math.min(free, Math.round(heroR.h * 0.5));
      if (h < 120) oilEl.style.display = "none";
      else oilEl.style.height = h + "px";
    }
    const fs = mobile ? 10.5 : vw < 1280 ? 12 : vw < 1700 ? 13 : 14;
    screen.style.setProperty("--fs", fs + "px");
    bootTop = boot.offsetTop; bootLen = Math.max(1, boot.offsetHeight - vh);
    chapGeo = chapters.map((c) => ({ el: c, top: c.offsetTop + c.offsetParent.offsetTop, h: c.offsetHeight, scene: c.dataset.scene }));
    moreTop = more.offsetTop; moreEnd = moreTop + more.offsetHeight;
    // banner start (untransformed) and its destination: the header wordmark
    banner.style.transform = "none";
    const hero = banner.offsetParent;
    bannerBase = { x: hero.offsetLeft + banner.offsetLeft, y: hero.offsetTop + banner.offsetTop, w: banner.offsetWidth, h: banner.offsetHeight };
    stops = settledStops();
    lastBp = -1; lastScene = "";
    frame();
  }

  // The scroll positions where nothing is moving: the welcome screen, its caption, each settled state of each
  // chapter, then the palette. They come from the same data that drives frame(), so a scene edit moves them too.
  function settledStops() {
    const out = [0, Math.round(bootTop + CAPTION * bootLen)];
    const fadeAt = moreTop - vh * 0.95; // frame(): the screen starts to fade here
    for (const c of chapGeo) {
      const name = c.scene;
      const busy = $$(".r", scenes[name]).map((el) => {
        const a = parseFloat(el.style.getPropertyValue("--a")) || 0;
        return [a, a + REVEAL];
      });
      const points = (STEPS[name] || []).slice();
      for (const t of typed) if (t.scene === name) { busy.push([t.from, t.to]); points.push(t.clear); }
      if (name === "core") busy.push([CORE_OFF[0], CORE_OFF[1]], [CORE_OFF[2], CORE_OFF[3]]);
      const hi = Math.min(1, (fadeAt + vh * 0.5 - c.top) / c.h) - 0.005;
      for (const cp of settledPoints(busy, points, 0, hi)) out.push(Math.round(c.top + cp * c.h - vh * 0.5));
    }
    out.push(targetY("#more"));
    return out.filter((y, i, a) => i === 0 || y > a[i - 1] + 8);
  }
  // One point inside each interval of progress where no range is animating and no discrete step changes.
  function settledPoints(busy, points, lo, hi) {
    const cuts = busy.concat(points.map((p) => [p, p])).filter(([a, b]) => b > lo && a < hi).sort((p, q) => p[0] - q[0]);
    const out = [];
    let at = lo;
    const keep = (end) => { if (end - at >= 0.015) out.push(at + Math.min((end - at) / 2, 0.05)); };
    for (const [a, b] of cuts) { keep(a); at = Math.max(at, b); }
    keep(hi);
    return out;
  }

  function setRect(r) {
    const s = screen.style;
    s.left = r.x + "px"; s.top = r.y + "px"; s.width = r.w + "px"; s.height = r.h + "px"; s.borderRadius = r.r + "px";
  }

  function frame() {
    raf = 0;
    const y = window.scrollY;
    const bp = clamp((y - bootTop) / bootLen);

    // boot choreography
    boot.style.setProperty("--boot", bp.toFixed(4));
    screen.style.setProperty("--boot", bp.toFixed(4));
    boot.classList.toggle("is-gone", bp > 0.16);
    hero.inert = bp > 0.16; // a faded install row takes no focus

    // banner flies into the header wordmark
    if (bannerBase) {
      const e = ease(clamp((bp - 0.03) / 0.21));
      if (e <= 0) { banner.style.transform = "none"; banner.style.opacity = "1"; }
      else {
        const tx = heroR.x + brand.offsetLeft, ty = heroR.y + brand.offsetTop + 1;
        const sc = brand.offsetWidth / bannerBase.w;
        const s = lerp(1, sc, e);
        banner.style.transform = `translate(${lerp(0, tx - bannerBase.x, e)}px, ${lerp(0, ty - bannerBase.y, e)}px) scale(${s})`;
        banner.style.opacity = String(1 - clamp((e - 0.82) / 0.18));
      }
      brand.style.opacity = String(clamp((bp - 0.22) * 25));
    }

    // screen geometry: full-bleed welcome → right column workspace → fades before the palette
    const g = ease(clamp((bp - 0.62) / 0.3));
    const r = {
      x: lerp(heroR.x, chapR.x, g), y: lerp(heroR.y, chapR.y, g), w: lerp(heroR.w, chapR.w, g), h: lerp(heroR.h, chapR.h, g), r: lerp(heroR.r, chapR.r, g),
    };
    setRect(r);
    screen.style.setProperty("--tree", String(1 - g));
    const f = clamp((vh * 0.95 - (moreTop - y)) / (vh * 0.6));
    screen.style.opacity = String(1 - f);
    screen.style.transform = f > 0 ? `translateY(${-Math.round(f * 40)}px)` : "";
    screen.style.visibility = f >= 1 ? "hidden" : "visible";

    // chapters → scenes: the screen shows the last chapter the reader reached, also while it fades out
    let scene = "chat";
    const mid = y + vh * 0.5;
    for (const c of chapGeo) {
      scenes[c.scene].style.setProperty("--cp", clamp((mid - c.top) / c.h).toFixed(4));
      const past = mid >= c.top + c.h;
      c.el.classList.toggle("is-active", f <= 0 && mid >= c.top && !past);
      c.el.classList.toggle("is-past", f > 0 || past);
      if (mid >= c.top) scene = c.scene;
    }
    const chatCp = clamp((bp - 0.3) / 0.4);
    scenes.chat.style.setProperty("--cp", chatCp.toFixed(4));

    if (scene !== lastScene) {
      screen.dataset.scene = scene;
      rail.forEach((el, i) => el.classList.toggle("is-on", i === RAIL[scene]));
      lastScene = scene;
    }

    // discrete steps per scene
    for (const [name, th] of Object.entries(STEPS)) {
      const cp = parseFloat(scenes[name].style.getPropertyValue("--cp")) || 0;
      const step = th.filter((t) => cp >= t).length;
      if (scenes[name].dataset.step !== String(step)) scenes[name].dataset.step = String(step);
    }
    const idx = ORDER.indexOf(scene);
    const lspStep = +scenes.lsp.dataset.step;
    screen.dataset.problems = idx < 3 || (idx === 3 && lspStep < 3) ? "one" : "clean";

    // typing in composers
    for (const t of typed) {
      const cp = t.scene === "chat" ? chatCp : parseFloat(scenes[t.scene].style.getPropertyValue("--cp")) || 0;
      let n = Math.round(t.text.length * clamp((cp - t.from) / (t.to - t.from)));
      if (cp >= t.clear) n = 0;
      if (n !== t.n) { t.el.textContent = t.text.slice(0, n); t.comp.classList.toggle("has-text", n > 0); t.n = n; }
    }

    // window closes and reopens during the core chapter
    const ccp = parseFloat(scenes.core.style.getPropertyValue("--cp")) || 0;
    let off = 0;
    if (scene === "core") {
      const [close, closed, open, opened] = CORE_OFF;
      if (ccp >= close && ccp < closed) off = (ccp - close) / (closed - close);
      else if (ccp >= closed && ccp < open) off = 1;
      else if (ccp >= open && ccp < opened) off = 1 - (ccp - open) / (opened - open);
    }
    screen.style.setProperty("--off", off.toFixed(4));

    // nav: show where the reader is
    const navOn = bp < 0.16 ? "#install" : y + vh * 0.5 >= moreEnd ? "" : scene === "memory" && f <= 0 ? "#memory" : "#features";
    if (navOn !== lastNav) {
      navLinks.forEach((a) => (a.getAttribute("href") === navOn ? a.setAttribute("aria-current", "true") : a.removeAttribute("aria-current")));
      lastNav = navOn;
    }

    // the live art only runs while it is visible
    oil.setActive(bp < 0.32 && f < 1);
    lastBp = bp;
  }
  const request = () => { if (!raf) raf = requestAnimationFrame(frame); };

  /* ───────────── Live art: an ASCII drop of crude oil ───────────── */
  const oil = (() => {
    const canvas = $("#oil");
    const ctx = canvas.getContext("2d");
    const RAMP = [" ", ".", ".", ":", "-", "=", "+", "*", "#", "%", "@", "@"];
    const PAL = ["#000", "#2b1709", "#3e210c", "#583110", "#7b4516", "#a1591d", "#c76c26", "#e27a31", "#f0883e", "#f6a666", "#fbc596", "#ffe6cf"];
    const PROMPT = "#e6edf3";
    let W = 0, H = 0, cw = 8, chh = 14, cols = 0, rows = 0, running = false, rafId = 0, lastT = 0;
    let lx = -0.5, ly = -0.65, tx = -0.5, ty = -0.65;
    const ripples = []; const drips = []; let nextDrip = 1.4, simT = 0;
    const buckets = PAL.map(() => []);
    const promptCells = [];

    function resize() {
      const dpr = Math.min(2, window.devicePixelRatio || 1);
      W = canvas.clientWidth; H = canvas.clientHeight; // layout size: ignores the power-on transform
      if (!W || !H) return;
      canvas.width = Math.round(W * dpr); canvas.height = Math.round(H * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      const fs = clamp(W / 64, 8, 13);
      ctx.font = `700 ${fs}px "Crude Mono", ui-monospace, monospace`;
      ctx.textBaseline = "top";
      cw = ctx.measureText("M").width || fs * 0.6; chh = fs * 1.16;
      cols = Math.floor(W / cw); rows = Math.floor(H / chh);
      draw(simT);
    }

    // teardrop: tip on top, body of revolution with half-width w(v), v ∈ [-1, 1]
    const WS = 0.93;
    function halfWidth(v, breathe) {
      if (v <= -1 || v >= 1) return 0;
      const th = Math.acos(-v);
      return WS * breathe * Math.sin(th) * Math.sin(th / 2);
    }
    function shadeDrop(u, v, breathe, L, t) {
      const w = halfWidth(v, breathe);
      if (w <= 0 || Math.abs(u) >= w) return -1;
      const dw = (halfWidth(v + 0.01, breathe) - halfWidth(v - 0.01, breathe)) / 0.02;
      const z = Math.sqrt(w * w - u * u);
      let nx = u, ny = -w * dw, nz = z;
      const nl = Math.hypot(nx, ny, nz) || 1; nx /= nl; ny /= nl; nz /= nl;
      const diff = Math.max(0, nx * L[0] + ny * L[1] + nz * L[2]);
      let hx = L[0], hy = L[1], hz = L[2] + 1; const hl = Math.hypot(hx, hy, hz); hx /= hl; hy /= hl; hz /= hl;
      const spec = Math.pow(Math.max(0, nx * hx + ny * hy + nz * hz), 30);
      const rim = Math.pow(1 - nz, 2.4);
      const swirl = 0.05 * Math.sin(u * 9 + t * 0.9) * Math.sin(v * 7 - t * 0.6);
      return 0.16 + 0.2 * diff + 1.15 * spec + 0.5 * rim * (0.55 + 0.45 * diff) + swirl;
    }
    function segDist(px, py, ax, ay, bx, by) {
      const dx = bx - ax, dy = by - ay; const t = clamp(((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy));
      return Math.hypot(px - (ax + t * dx), py - (ay + t * dy));
    }

    function draw(t) {
      if (!cols) return;
      ctx.clearRect(0, 0, W, H);
      for (const b of buckets) b.length = 0;
      promptCells.length = 0;
      const S = H * 0.235;                              // half height of the drop in px
      const cx = W * 0.5, cy = H * 0.36 + Math.sin(t * 0.9) * 3;
      const horizon = H * 0.72;
      const breathe = 1 + 0.025 * Math.sin(t * 1.3);
      lx += (tx - lx) * 0.06; ly += (ty - ly) * 0.06;
      const ll = Math.hypot(lx, ly, 0.95); const L = [lx / ll, ly / ll, 0.95 / ll];
      const cursorOn = Math.floor(t * 1.9) % 2 === 0;
      const thick = 0.085;
      // ripples age out
      for (let i = ripples.length - 1; i >= 0; i--) if (t - ripples[i].t > 7) ripples.splice(i, 1);

      for (let j = 0; j < rows; j++) {
        const py = (j + 0.5) * chh;
        for (let i = 0; i < cols; i++) {
          const px = (i + 0.5) * cw;
          let I = -1, isPrompt = false;
          if (py < horizon) {
            const u = (px - cx) / S, v = (py - cy) / S;
            I = shadeDrop(u, v, breathe, L, t);
            if (I >= 0) {
              const d1 = Math.min(segDist(u, v, -0.44, 0.02, -0.12, 0.27), segDist(u, v, -0.12, 0.27, -0.44, 0.52));
              const d2 = cursorOn ? segDist(u, v, 0.02, 0.54, 0.40, 0.54) : 9;
              if (d1 < thick || d2 < thick) isPrompt = true;
              I = Math.max(I, 0.2);
            }
            // falling drips
            for (const d of drips) {
              const ddx = (px - cx) / (S * 0.07), ddy = (py - d.y) / (S * 0.11);
              const q = ddx * ddx + ddy * ddy;
              if (q < 1) I = Math.max(I, 0.55 + (1 - q) * 0.45);
            }
          } else {
            const depth = (py - horizon) / (H - horizon);
            let h = 0.07 * Math.sin(px * 0.03 + t * 0.9 + depth * 7) * (1 - depth * 0.5);
            for (const rp of ripples) {
              const a = t - rp.t; const front = a * rp.speed;
              const rr = Math.hypot(px - rp.x, (py - rp.y) * 3.1);
              const gg = rr - front;
              h += rp.amp * Math.exp(-a * 0.5) * Math.cos(gg * 0.17) * Math.exp(-(gg * gg) / 1800);
            }
            // reflection of the drop, distorted by the surface
            const ry = horizon - (py - horizon) * 1.3 + h * 14;
            const ru = (px + h * 12 - cx) / S, rv = (ry - cy) / S;
            const refl = shadeDrop(ru, rv, breathe, L, t);
            I = (refl > 0 ? refl * 0.5 * (1 - depth * 0.55) : 0) + Math.max(0, h) * 0.85 + 0.035 + 0.05 * (1 - depth);
            if (Math.abs(py - horizon) < chh * 0.6) I = Math.max(I, 0.14);
            const edge = Math.abs(px - cx) / (W * 0.5);
            I *= Math.max(0, 1 - edge * edge * edge);
          }
          if (I < 0.035) continue;
          if (isPrompt) { promptCells.push(px - cw / 2, py - chh / 2); continue; }
          const lvl = Math.min(11, Math.max(1, Math.floor(I * 11.5)));
          buckets[lvl].push(px - cw / 2, py - chh / 2);
        }
      }
      for (let k = 1; k < buckets.length; k++) {
        const b = buckets[k]; if (!b.length) continue;
        ctx.fillStyle = PAL[k]; const ch = RAMP[k];
        for (let m = 0; m < b.length; m += 2) ctx.fillText(ch, b[m], b[m + 1]);
      }
      ctx.fillStyle = PROMPT;
      for (let m = 0; m < promptCells.length; m += 2) ctx.fillText("▓", promptCells[m], promptCells[m + 1]);
    }

    function step(now) {
      rafId = 0;
      if (!running) return;
      const dt = Math.min(0.05, (now - lastT) / 1000 || 0.016);
      if (now - lastT < 30) { rafId = requestAnimationFrame(step); return; } // ~30 fps is plenty for ASCII
      lastT = now; simT += dt;
      const S = H * 0.235, cy = H * 0.36, horizon = H * 0.72;
      nextDrip -= dt;
      if (nextDrip <= 0) { drips.push({ y: cy + S * 0.98, v: 0 }); nextDrip = 2.6 + Math.random() * 1.6; }
      for (let i = drips.length - 1; i >= 0; i--) {
        const d = drips[i]; d.v += 900 * dt; d.y += d.v * dt;
        if (d.y > horizon + chh) { ripples.push({ x: W * 0.5, y: horizon + (H - horizon) * 0.22, t: simT, amp: 0.95, speed: 70 }); drips.splice(i, 1); }
      }
      draw(simT);
      rafId = requestAnimationFrame(step);
    }
    function setActive(on) {
      on = on && !document.hidden;
      if (on === running) return;
      running = on;
      if (running && !reduce) { lastT = performance.now(); rafId = requestAnimationFrame(step); }
      else if (rafId) { cancelAnimationFrame(rafId); rafId = 0; }
    }
    function pointer(e) {
      const rect = canvas.getBoundingClientRect();
      if (!rect.width) return;
      const cx = rect.left + rect.width * 0.5, cy = rect.top + rect.height * 0.36;
      tx = clamp((e.clientX - cx) / (rect.width * 0.5), -1.2, 1.2);
      ty = clamp((e.clientY - cy) / (rect.height * 0.5), -1.2, 1.2);
      const lxp = e.clientX - rect.left, lyp = e.clientY - rect.top, horizon = rect.height * 0.72;
      if (lxp > 0 && lxp < rect.width && lyp > horizon && lyp < rect.height) {
        const now = simT;
        if (!pointer.t || now - pointer.t > 0.18) { ripples.push({ x: lxp, y: lyp, t: now, amp: e.type === "pointerdown" ? 1.2 : 0.35, speed: 80 }); pointer.t = now; }
      }
    }
    addEventListener("pointermove", pointer, { passive: true });
    addEventListener("pointerdown", pointer, { passive: true });
    document.addEventListener("visibilitychange", () => { if (document.hidden) setActive(false); else request(); });
    return { resize, setActive, draw: () => draw(simT) };
  })();

  /* ───────────── Glitch: the wordmark misbehaves, rarely and briefly ───────────── */
  (() => {
    if (reduce) return;
    const GLYPHS = "▓▒░█▀▄▌▐■▪╳╱╲";
    const LETTERS = [[0, 8], [8, 16], [16, 25], [25, 33], [33, 41]]; // C R U D E
    const CURED = [0, 2, 1, 4, 3];                                    // same letters, other order
    const items = $$(".banner").map((el) => {
      const text = el.textContent;
      const lines = text.split("\n");
      const W = Math.max(...lines.map((l) => l.length));
      return { el, text, W, rows: lines.map((l) => l.padEnd(W)), visible: false };
    });
    if (!items.length || items[0].W !== 41) return;
    const anagram = items[0].rows.map((r) => CURED.map((i) => r.slice(LETTERS[i][0], LETTERS[i][1])).join(""));
    const rnd = (n) => Math.floor(Math.random() * n);
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

    function corrupt(rows, n) {
      const out = rows.map((r) => r.split(""));
      for (let k = 0; k < n; k++) {
        const y = rnd(out.length), x = rnd(out[y].length);
        if (out[y][x] !== " ") out[y][x] = GLYPHS[rnd(GLYPHS.length)];
      }
      return out.map((a) => a.join(""));
    }
    function tear(rows, bands) {
      const out = rows.slice();
      for (let k = 0; k < bands; k++) {
        const y = rnd(out.length), d = [-2, -1, 1, 2][rnd(4)], r = out[y];
        out[y] = d > 0 ? r.slice(r.length - d) + r.slice(0, r.length - d) : r.slice(-d) + r.slice(0, -d);
      }
      return out;
    }
    function paint(it, rows, split) {
      const t = rows.join("\n");
      it.el.textContent = t;
      if (split) {
        const a = rnd(70), b = rnd(70);
        it.el.dataset.g = t;
        it.el.style.setProperty("--gx", (0.18 + Math.random() * 0.3).toFixed(2) + "ch");
        it.el.style.setProperty("--g1", `inset(${a}% 0 ${Math.max(0, 100 - a - 18 - rnd(30))}% 0)`);
        it.el.style.setProperty("--g2", `inset(${b}% 0 ${Math.max(0, 100 - b - 18 - rnd(30))}% 0)`);
        it.el.classList.add("is-glitch");
      } else it.el.classList.remove("is-glitch");
    }
    function restore(it) {
      it.el.textContent = it.text;
      it.el.classList.remove("is-glitch");
      delete it.el.dataset.g;
    }
    async function stutter(it) {
      for (let i = 0, n = 2 + rnd(3); i < n; i++) { paint(it, corrupt(tear(it.rows, 1 + rnd(2)), 3 + rnd(8)), true); await sleep(40 + rnd(45)); }
      restore(it);
    }
    async function decay(it) {
      for (const n of [5, 12, 22, 9]) { paint(it, corrupt(it.rows, n), n > 10); await sleep(50); }
      restore(it);
    }
    async function cured(it) {
      paint(it, corrupt(tear(it.rows, 2), 6), true); await sleep(50);
      paint(it, anagram, true); await sleep(70);
      paint(it, anagram, false); await sleep(90);
      paint(it, corrupt(tear(anagram, 2), 6), true); await sleep(45);
      restore(it);
    }

    const io = new IntersectionObserver((es) => es.forEach((e) => {
      const it = items.find((i) => i.el === e.target); if (it) it.visible = e.isIntersecting;
    }));
    items.forEach((it) => { if (it.el !== banner) io.observe(it.el); });
    const canRun = (it) => !document.hidden && (it.el === banner ? lastBp < 0.02 : it.visible);

    let count = 0, lastCured = -Infinity;
    async function run() {
      const it = items.find(canRun);
      if (it) {
        count++;
        const r = Math.random(), now = performance.now();
        if (count > 1 && now - lastCured > 30000 && r < 0.25) { lastCured = now; await cured(it); }
        else if (r < 0.7) { await stutter(it); if (Math.random() < 0.3) { await sleep(160 + rnd(120)); await stutter(it); } }
        else await decay(it);
      }
      setTimeout(run, 6000 + Math.random() * 9000);
    }
    setTimeout(run, 3200 + Math.random() * 2400);
  })();

  /* ───────────── Palette ───────────── */
  const palette = $("[data-palette]");
  if (palette) {
    const input = $("[data-filter]", palette);
    const items = $$("[data-list] li", palette).map((li) => {
      const h3 = $("h3", li);
      return { li, h3, title: h3.textContent, hay: li.textContent.toLowerCase() };
    });
    const empty = $("[data-empty]", palette);
    let sel = -1;
    const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
    const visible = () => items.filter((it) => !it.li.hidden);
    function select(i) {
      const v = visible();
      items.forEach((it) => it.li.classList.remove("is-sel"));
      if (!v.length) { sel = -1; return; }
      sel = (i + v.length) % v.length;
      v[sel].li.classList.add("is-sel");
      v[sel].li.scrollIntoView({ block: "nearest" });
    }
    function filter() {
      const q = input.value.trim().toLowerCase();
      const words = q.split(/\s+/).filter(Boolean);
      let shown = 0;
      for (const it of items) {
        const hit = words.every((w) => it.hay.includes(w));
        it.li.hidden = !hit;
        if (hit) shown++;
        let html = esc(it.title);
        if (words.length) {
          const re = new RegExp("(" + words.map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|") + ")", "gi");
          html = html.replace(re, "<mark>$1</mark>");
        }
        it.h3.innerHTML = html;
      }
      empty.hidden = shown > 0;
      if (words.length) select(0);
      else { sel = -1; items.forEach((it) => it.li.classList.remove("is-sel")); }
    }
    input.addEventListener("input", filter);
    input.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown") { e.preventDefault(); select(sel + 1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); select(sel < 0 ? -1 : sel - 1); }
      else if (e.key === "Escape") { if (input.value) { input.value = ""; filter(); } else input.blur(); } // then the keys scroll the page again
      else if (e.key === "Enter") { e.preventDefault(); window.open("https://github.com/opencrude/crude/blob/master/docs/README.md", "_blank", "noopener"); }
    });
  }

  /* ───────────── Keys: c copies the install command, / opens the palette, 1–4 follow the header,
     and the scroll keys move from one settled state of the story to the next ───────────── */
  const NEXT = { ArrowDown: 1, PageDown: 1, " ": 1, ArrowUp: -1, PageUp: -1 };
  let keyTarget = null; // { y, t }: where the last key sent the page, while it is still moving there
  function keyBase() {
    if (keyTarget && performance.now() - keyTarget.t < 900 && Math.abs(window.scrollY - keyTarget.y) > 2) return keyTarget.y;
    return window.scrollY;
  }
  function step(dir) {
    const y = keyBase(), last = stops[stops.length - 1];
    // Below the story the palette and the questions are ordinary content: the browser scrolls them.
    if (dir > 0 ? y >= last - 8 : y > last + 8) return false;
    const to = dir > 0 ? stops.find((s) => s > y + 8) : stops.filter((s) => s < y - 8).pop();
    if (to === undefined) return false;
    keyTarget = { y: to, t: performance.now() };
    window.scrollTo({ top: to, behavior: reduce ? "auto" : "smooth" });
    return true;
  }
  const typing = (el) => el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName));
  const pressable = (el) => el && /^(BUTTON|SUMMARY)$/.test(el.tagName); // Space presses them
  const scrollsItself = (el) => el && el !== document.body && el !== document.documentElement &&
    el.scrollHeight > el.clientHeight && /(auto|scroll)/.test(getComputedStyle(el).overflowY);
  addEventListener("keydown", (e) => {
    if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey) return;
    const el = document.activeElement;
    if (typing(el)) return;
    if (e.key in NEXT) {
      if (scrollsItself(el) || (e.key === " " && pressable(el))) return;
      if (step(e.key === " " && e.shiftKey ? -1 : NEXT[e.key])) e.preventDefault();
    } else if (e.key === "/") { e.preventDefault(); const i = $("[data-filter]"); i.scrollIntoView({ block: "center", behavior: reduce ? "auto" : "smooth" }); setTimeout(() => i.focus({ preventScroll: true }), reduce ? 0 : 450); }
    else if ((e.key === "c" || e.key === "C") && lastBp < 0.16) {
      const b = $(`#install .ins-row[data-os="${detected}"] [data-copy]`);
      if (b) copyCommand(b);
    } else if (!e.shiftKey && /^[1-9]$/.test(e.key)) {
      const a = $$(".nav-links a").find((l) => { const k = $(".k", l); return k && k.textContent.trim() === e.key; });
      if (a) { e.preventDefault(); a.click(); }
    }
  });
  // A wheel, a drag or a link takes over from a key that is still scrolling.
  addEventListener("wheel", () => { keyTarget = null; }, { passive: true });
  addEventListener("touchstart", () => { keyTarget = null; }, { passive: true });

  /* ───────────── In-page links ───────────── */
  function targetY(hash) {
    if (hash === "#install" || hash === "#main" || hash === "#boot") return 0;
    const el = hash.length > 1 && document.getElementById(hash.slice(1));
    if (!el) return null;
    const ch = el.classList.contains("chapter") ? el : el.id === "features" ? chapters[0] : null;
    if (ch) { const g = chapGeo.find((c) => c.el === ch); return Math.round(g.top + g.h * 0.32 - vh * 0.5); }
    return Math.max(0, Math.round(el.getBoundingClientRect().top + window.scrollY - 72));
  }
  function go(hash, smooth) {
    const y = targetY(hash);
    if (y === null) return false;
    keyTarget = null;
    window.scrollTo({ top: y, behavior: smooth && !reduce ? "smooth" : "auto" });
    history.replaceState(null, "", hash === "#install" ? location.pathname : hash);
    return true;
  }
  document.addEventListener("click", (e) => {
    const a = e.target.closest && e.target.closest('a[href^="#"]');
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey) return;
    if (go(a.getAttribute("href"), true)) e.preventDefault();
  });

  /* ───────────── Start ───────────── */
  addEventListener("scroll", request, { passive: true });
  addEventListener("resize", () => { layout(); oil.resize(); });
  const start = () => {
    layout(); oil.resize();
    if (!reduce) screen.classList.add("is-power");
    request();
  };
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(() => { layout(); oil.resize(); });
  start();
  if (location.hash) requestAnimationFrame(() => go(location.hash, false));
})();
