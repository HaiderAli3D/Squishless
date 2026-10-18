// Caire Studio UI. Vanilla ES module, no dependencies.
// Sections: helpers, state, theme, source loading, settings, stage geometry
// and rendering, mask painter, jobs and live stream, result view, history,
// keyboard, init.

/* =========================================================================
   1. Helpers
   ========================================================================= */
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

const store = {
  get(key, fallback) {
    try {
      const raw = localStorage.getItem(key);
      return raw == null ? fallback : JSON.parse(raw);
    } catch (_) { return fallback; }
  },
  set(key, value) {
    try { localStorage.setItem(key, JSON.stringify(value)); } catch (_) { /* storage unavailable */ }
  },
};

const MAX_BYTES = 25 * 1024 * 1024;
const MAX_PIXELS = 40e6;
const MAX_SIDE = 10000;    // server limit for a requested width or height (maxTargetSide)
const FULL_RESULTS = 3;    // history entries that keep full-resolution pixels in memory
const LOST_AFTER_MS = 10000; // give up on a dropped live stream after this long
const RULER = 24;       // ruler band thickness, CSS px
const STAGE_PAD = 20;   // gap between rulers and the image box

const fmtTime = (ms) => {
  if (!Number.isFinite(ms) || ms < 0) return '0 s';
  if (ms < 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`;
  const m = Math.floor(ms / 60000);
  const s = Math.round((ms % 60000) / 1000);
  return `${m}:${String(s).padStart(2, '0')} min`;
};
const dims = (w, h) => `${w} × ${h}`;

function makeCanvas(w, h) {
  const c = document.createElement('canvas');
  c.width = Math.max(1, Math.round(w));
  c.height = Math.max(1, Math.round(h));
  return c;
}

function isSupportedFile(blob, name) {
  if (/^image\/(jpeg|pjpeg|png|bmp|x-ms-bmp|x-bmp|gif)$/i.test(blob.type || '')) return true;
  return /\.(jpe?g|png|bmp|gif)$/i.test(name || '');
}

async function decodeImage(blob) {
  // Go's decoders ignore EXIF orientation, so preview the raw pixel grid to
  // keep masks aligned with what the server sees.
  try { return await createImageBitmap(blob, { imageOrientation: 'none' }); }
  catch (_) { return await createImageBitmap(blob); }
}

async function readError(res) {
  let msg = '';
  try {
    const text = await res.text();
    try { msg = JSON.parse(text).error || text; } catch (_) { msg = text; }
  } catch (_) { /* ignore */ }
  msg = (msg || '').trim();
  return msg || `The server answered with HTTP ${res.status}.`;
}

/* =========================================================================
   2. Elements and state
   ========================================================================= */
const el = {
  sourceMeta: $('#sourceMeta'),
  fileInputs: [$('#fileInput'), $('#fileInput2')],
  themeToggle: $('#themeToggle'),
  samples: $('#samples'),
  form: $('#controls'),
  w: $('#w'), h: $('#h'),
  unitPct: $('#unitPct'),
  unitHint: $('#unitHint'),
  keepAspect: $('#keepAspect'),
  square: $('#square'),
  plan: $('#plan'),
  sizeError: $('#sizeError'),
  blur: $('#blur'), blurOut: $('#blurOut'),
  sobel: $('#sobel'), sobelOut: $('#sobelOut'),
  face: $('#face'), faceAngle: $('#faceAngle'), faceAngleOut: $('#faceAngleOut'), faceAngleWrap: $('#faceAngleWrap'),
  debug: $('#debug'), seamStyle: $('#seamStyle'), seamColor: $('#seamColor'), seamColorValue: $('#seamColorValue'),
  runBtn: $('#runBtn'), runLabel: $('#runLabel'), cancelBtn: $('#cancelBtn'),
  runError: $('#runError'), runErrorTitle: $('#runErrorTitle'), runErrorMsg: $('#runErrorMsg'),
  tabs: $$('.tab'),
  maskTools: $('#maskTools'), resultTools: $('#resultTools'),
  brushSize: $('#brushSize'), brushOut: $('#brushOut'), showMasks: $('#showMasks'), clearMasks: $('#clearMasks'),
  compareOn: $('#compareOn'), downloadBtn: $('#downloadBtn'),
  stage: $('#stage'), canvas: $('#stageCanvas'), ring: $('#brushRing'), divider: $('#divider'),
  empty: $('#emptyState'), loadSampleBtn: $('#loadSampleBtn'),
  progress: $('#progress'), progressFill: $('#progressFill'),
  statusText: $('#statusText'), roSeams: $('#roSeams'), roDone: $('#roDone'), roTotal: $('#roTotal'),
  roPass: $('#roPass'), roSize: $('#roSize'), roElapsed: $('#roElapsed'), roRate: $('#roRate'),
  historyStrip: $('#historyStrip'),
  dropOverlay: $('#dropOverlay'), toast: $('#toast'),
};

const state = {
  source: null,          // { id, name, blob, width, height, bitmap, protect, remove }
  samples: [],
  view: 'source',        // 'source' | 'live' | 'result'
  job: null,             // current run, see run()
  result: null,          // currently displayed result
  history: [],
  brush: { mode: 'protect', size: 32, show: true },
  compare: { on: true, pick: 'carved', pos: 0.5 },
  stageSize: { w: 0, h: 0 },
  dpr: 1,
  geom: null,            // last geometry used to draw the stage
};
let sourceSeq = 0;
let jobSeq = 0;

/* =========================================================================
   3. Theme
   ========================================================================= */
const lightQuery = window.matchMedia ? window.matchMedia('(prefers-color-scheme: light)') : null;
let tokenCache = null;

function effectiveTheme() {
  const t = document.documentElement.dataset.theme;
  if (t === 'light' || t === 'dark') return t;
  return lightQuery && lightQuery.matches ? 'light' : 'dark';
}
function syncThemeButton() {
  const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
  el.themeToggle.setAttribute('aria-label', `Switch to ${next} theme`);
}
function toggleTheme() {
  const next = effectiveTheme() === 'dark' ? 'light' : 'dark';
  document.documentElement.dataset.theme = next;
  store.set('caire.theme', next);
  tokenCache = null;
  syncThemeButton();
  scheduleRender();
}
function tokens() {
  if (tokenCache) return tokenCache;
  const cs = getComputedStyle(document.documentElement);
  const v = (n) => cs.getPropertyValue(n).trim();
  tokenCache = {
    ruler: v('--ruler'), line: v('--line'), lineStrong: v('--line-strong'), text: v('--text'),
    muted: v('--muted'), faint: v('--faint'), amber: v('--amber'), amberInk: v('--amber-ink'),
    cyan: v('--cyan'), protect: v('--protect'), remove: v('--remove'), stage: v('--stage'),
    mono: v('--mono') || 'monospace', font: v('--font') || 'sans-serif',
  };
  return tokenCache;
}

/* =========================================================================
   4. Toast
   ========================================================================= */
let toastTimer = 0;
function toast(msg, kind = 'info') {
  el.toast.textContent = msg;
  el.toast.classList.toggle('is-error', kind === 'error');
  el.toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.toast.hidden = true; }, kind === 'error' ? 6000 : 3200);
}

/* =========================================================================
   5. Source loading (file picker, drop, paste, samples)
   ========================================================================= */
async function loadBlob(blob, name) {
  name = name || 'image';
  if (!isSupportedFile(blob, name)) {
    toast(`${name} isn't a JPEG, PNG, BMP or GIF file.`, 'error');
    return false;
  }
  if (blob.size > MAX_BYTES) {
    toast(`${name} is ${(blob.size / 1048576).toFixed(1)} MB. The limit is 25 MB.`, 'error');
    return false;
  }
  let bitmap;
  try { bitmap = await decodeImage(blob); }
  catch (_) {
    toast(`Couldn't read ${name}. The file may be damaged or in an unsupported format.`, 'error');
    return false;
  }
  const { width, height } = bitmap;
  if (width * height > MAX_PIXELS) {
    toast(`${name} is ${(width * height / 1e6).toFixed(1)} megapixels. The limit is 40.`, 'error');
    bitmap.close && bitmap.close();
    return false;
  }
  state.source = {
    id: ++sourceSeq, name, blob, width, height, bitmap,
    protect: makeCanvas(width, height),
    remove: makeCanvas(width, height),
  };
  onSourceChanged();
  setView('source');
  return true;
}

function onSourceChanged() {
  const s = state.source;
  el.empty.hidden = !!s;
  if (s) {
    el.sourceMeta.innerHTML = '';
    const n = document.createElement('span');
    n.className = 'source-meta-name';
    n.textContent = s.name;
    n.title = s.name;
    const d = document.createElement('span');
    d.className = 'source-meta-dims';
    d.textContent = `${dims(s.width, s.height)} px`;
    el.sourceMeta.append(n, d);
    el.w.placeholder = String(s.width);
    el.h.placeholder = String(s.height);
  }
  el.runBtn.disabled = !s;
  $$('[data-preset]').forEach((b) => { b.disabled = !s; });
  // A finished run on a different image no longer describes the stage.
  if (state.job && !isActive(state.job) && state.job.source !== s) {
    state.job = null;
    setTabEnabled('live', false);
    if (state.view === 'live') state.view = 'source';
  }
  refreshSettingsUI();
  if (!isActive(state.job)) updateTelemetry();
  scheduleRender();
}

function pickFile(input) {
  const f = input.files && input.files[0];
  input.value = '';
  if (f) loadBlob(f, f.name);
}

async function loadSamples() {
  try {
    const res = await fetch('/api/samples');
    if (!res.ok) throw new Error(String(res.status));
    const list = await res.json();
    state.samples = Array.isArray(list) ? list : [];
  } catch (_) {
    state.samples = [];
  }
  renderSamples();
}

function renderSamples() {
  el.samples.innerHTML = '';
  if (!state.samples.length) {
    const p = document.createElement('span');
    p.className = 'hint';
    p.textContent = 'No samples found. Open or drop your own image.';
    el.samples.append(p);
    el.loadSampleBtn.disabled = true;
    return;
  }
  el.loadSampleBtn.disabled = false;
  for (const s of state.samples) {
    const item = document.createElement('div');
    item.className = 'list-item';
    item.setAttribute('role', 'listitem');
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'sample';
    b.setAttribute('aria-label', `Load sample ${s.name}, ${s.width} by ${s.height} pixels`);
    const img = document.createElement('img');
    img.src = s.url; img.alt = ''; img.loading = 'lazy';
    const cap = document.createElement('span');
    cap.textContent = `${s.width}×${s.height}`;
    cap.title = s.name;
    b.append(img, cap);
    b.addEventListener('click', () => loadSample(s));
    item.append(b);
    el.samples.append(item);
  }
}

// The empty-state call to action loads the bundled sample.jpg, not the first sample by name.
function defaultSample() {
  return state.samples.find((s) => s.name.toLowerCase() === 'sample.jpg') || state.samples[0];
}

async function loadSample(s) {
  try {
    const res = await fetch(s.url);
    if (!res.ok) throw new Error(String(res.status));
    const blob = await res.blob();
    await loadBlob(blob, s.name);
  } catch (_) {
    toast(`Couldn't load the sample ${s.name}.`, 'error');
  }
}

function setupDropAndPaste() {
  let depth = 0;
  const hasFiles = (e) => e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files');
  window.addEventListener('dragenter', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    depth++;
    el.dropOverlay.hidden = false;
  });
  window.addEventListener('dragover', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
  });
  window.addEventListener('dragleave', (e) => {
    if (!hasFiles(e)) return;
    depth = Math.max(0, depth - 1);
    if (!depth) el.dropOverlay.hidden = true;
  });
  window.addEventListener('drop', (e) => {
    if (!hasFiles(e)) return;
    e.preventDefault();
    depth = 0;
    el.dropOverlay.hidden = true;
    const f = e.dataTransfer.files && e.dataTransfer.files[0];
    if (f) loadBlob(f, f.name);
  });
  document.addEventListener('paste', (e) => {
    const items = (e.clipboardData && e.clipboardData.items) || [];
    for (const it of items) {
      if (it.kind === 'file' && /^image\//.test(it.type)) {
        const f = it.getAsFile();
        if (!f) continue;
        e.preventDefault();
        const ext = (it.type.split('/')[1] || 'png').replace('jpeg', 'jpg');
        loadBlob(f, f.name && f.name !== 'image.png' ? f.name : `pasted-image.${ext}`);
        return;
      }
    }
  });
}

/* =========================================================================
   6. Settings: read, write, validate, plan
   ========================================================================= */
const DEFAULTS = {
  width: '', height: '', unit: 'px', keepAspect: false, square: false,
  blur: 4, sobel: 2, face: false, faceAngle: 0,
  debug: false, shape: 'circle', seamColor: '#ff0000', format: 'png',
};

const radioValue = (name) => { const r = $(`input[name="${name}"]:checked`); return r ? r.value : ''; };
const setRadio = (name, value) => { const r = $(`input[name="${name}"][value="${value}"]`); if (r) r.checked = true; };

function readForm() {
  return {
    width: el.w.value.trim(),
    height: el.h.value.trim(),
    unit: radioValue('unit') || 'px',
    keepAspect: el.keepAspect.checked,
    square: el.square.checked,
    blur: Number(el.blur.value),
    sobel: Number(el.sobel.value),
    face: el.face.checked,
    faceAngle: Number(el.faceAngle.value),
    debug: el.debug.checked,
    shape: radioValue('shape') || 'circle',
    seamColor: el.seamColor.value,
    format: radioValue('format') || 'png',
  };
}

function applyForm(ui) {
  const u = { ...DEFAULTS, ...ui };
  el.w.value = u.width; el.h.value = u.height;
  setRadio('unit', u.unit === '%' ? '%' : 'px');
  el.keepAspect.checked = !!u.keepAspect;
  el.square.checked = !!u.square;
  el.blur.value = clamp(Number(u.blur) || 0, 0, 20);
  el.sobel.value = clamp(Number(u.sobel) || 0, 0, 20);
  el.face.checked = !!u.face;
  el.faceAngle.value = clamp(Number(u.faceAngle) || 0, -180, 180);
  el.debug.checked = !!u.debug;
  setRadio('shape', u.shape === 'line' ? 'line' : 'circle');
  el.seamColor.value = /^#[0-9a-f]{6}$/i.test(u.seamColor) ? u.seamColor : '#ff0000';
  setRadio('format', u.format === 'jpg' ? 'jpg' : 'png');
  refreshSettingsUI();
}

const intOf = (s) => (s === '' ? 0 : Number(s));

function estimateTarget(ui, src) {
  const W = src.width, H = src.height;
  let w = intOf(ui.width), h = intOf(ui.height);
  if (!Number.isFinite(w) || w < 0) w = 0;
  if (!Number.isFinite(h) || h < 0) h = 0;
  if (ui.unit === '%') {
    w = w ? Math.round(W * w / 100) : W;
    h = h ? Math.round(H * h / 100) : H;
  } else {
    w = w || W;
    h = h || H;
  }
  if (ui.square) { const s = Math.min(w, h); w = s; h = s; }
  return { w: Math.max(1, w), h: Math.max(1, h) };
}

function validate(ui, src) {
  const out = { ok: false, size: '', general: '', api: null };
  if (!src) { out.general = 'Load an image first.'; return out; }
  const pct = ui.unit === '%';
  const w = intOf(ui.width), h = intOf(ui.height);
  const bad = (v) => !Number.isInteger(v) || v < 0;
  if (bad(w) || bad(h)) { out.size = 'Width and height must be whole numbers, or left empty to keep that side.'; return out; }
  if (!w && !h) { out.size = 'Enter a new width or height. Leave a side empty to keep it unchanged.'; return out; }
  if (ui.square && (!w || !h)) { out.size = 'Square needs both a width and a height.'; return out; }
  if (pct) {
    if ((w && w >= 100) || (h && h >= 100)) { out.size = 'Percent is the share of the original to keep, so use 1–99. Switch to px to enlarge.'; return out; }
  } else {
    if (w > MAX_SIDE || h > MAX_SIDE) { out.size = `Keep each side at ${MAX_SIDE.toLocaleString('en-US')} px or less.`; return out; }
    if (!ui.square && (w === 0 || w === src.width) && (h === 0 || h === src.height)) {
      out.size = `That is the source size already (${dims(src.width, src.height)}). Change the width or height.`;
      return out;
    }
  }
  if (!(ui.blur >= 0 && ui.blur <= 50) || !(ui.sobel >= 0 && ui.sobel <= 50)) { out.general = 'Blur and sobel must be between 0 and 50.'; return out; }
  if (!(ui.faceAngle >= -180 && ui.faceAngle <= 180)) { out.general = 'Face angle must be between −180° and 180°.'; return out; }
  out.ok = true;
  out.api = {
    width: w, height: h, percentage: pct, square: !!ui.square,
    blur: ui.blur, sobel: ui.sobel, debug: !!ui.debug,
    shape: ui.shape === 'line' ? 'line' : 'circle',
    seamColor: ui.seamColor, face: !!ui.face, faceAngle: ui.faceAngle,
  };
  return out;
}

function refreshSettingsUI() {
  const ui = readForm();
  const src = state.source;
  el.blurOut.textContent = String(ui.blur);
  el.sobelOut.textContent = String(ui.sobel);
  el.faceAngleOut.textContent = `${ui.faceAngle}°`;
  el.faceAngle.disabled = !ui.face;
  el.faceAngleWrap.classList.toggle('is-disabled', !ui.face);
  el.seamStyle.classList.toggle('is-disabled', !ui.debug);
  $$('input', el.seamStyle).forEach((i) => { i.disabled = !ui.debug; });
  el.seamColorValue.textContent = ui.seamColor;
  $$('[data-unit-label]').forEach((s) => { s.textContent = ui.unit; });

  const pct = ui.unit === '%';
  el.w.max = pct ? '99' : String(MAX_SIDE);
  el.h.max = pct ? '99' : String(MAX_SIDE);
  el.w.min = pct ? '1' : '0';
  el.h.min = pct ? '1' : '0';
  if (src) {
    el.w.placeholder = pct ? '100' : String(src.width);
    el.h.placeholder = pct ? '100' : String(src.height);
  }

  // Percent only shrinks: disable it while the px values enlarge.
  const enlarging = !!src && !pct && (intOf(ui.width) > src.width || intOf(ui.height) > src.height);
  el.unitPct.disabled = enlarging;
  el.unitHint.textContent = enlarging
    ? 'Percent mode is off while enlarging. Caire adds seams to grow an image, and percent only shrinks.'
    : 'In % mode, the number is how much of the original to keep, so 70 means 70% of the width. Percent only shrinks.';

  // Live validation feedback for the size block.
  const v = validate(ui, src);
  const sizeMsg = src && (ui.width !== '' || ui.height !== '') ? v.size : '';
  el.sizeError.hidden = !sizeMsg;
  el.sizeError.textContent = sizeMsg;
  el.w.setAttribute('aria-invalid', sizeMsg ? 'true' : 'false');
  el.h.setAttribute('aria-invalid', sizeMsg ? 'true' : 'false');

  renderPlan(ui, src, v);
  store.set('caire.params', ui);
  scheduleRender();
}

function renderPlan(ui, src, v) {
  el.plan.innerHTML = '';
  if (!src || !v.ok) return;
  const t = estimateTarget(ui, src);
  const dw = t.w - src.width, dh = t.h - src.height;
  const line = document.createElement('div');
  line.innerHTML = `${dims(src.width, src.height)} → <b>${dims(t.w, t.h)}</b>`;
  el.plan.append(line);
  const parts = [];
  if (dw) parts.push(`${Math.abs(dw)} ${dw < 0 ? 'columns removed' : 'columns added'}`);
  if (dh) parts.push(`${Math.abs(dh)} ${dh < 0 ? 'rows removed' : 'rows added'}`);
  const note = document.createElement('span');
  note.className = 'plan-note';
  if (dw < 0 && dh < 0 && !ui.square) note.textContent = 'Both sides shrink, so Caire scales down first and carves fewer seams than this.';
  else note.textContent = parts.join(', ') || 'No change.';
  el.plan.append(note);
}

function onSizeInput(which) {
  const src = state.source;
  if (src && el.keepAspect.checked && !el.square.checked) {
    const pct = radioValue('unit') === '%';
    const from = which === 'w' ? el.w : el.h;
    const to = which === 'w' ? el.h : el.w;
    const v = intOf(from.value.trim());
    if (!from.value.trim() || !Number.isFinite(v)) to.value = '';
    else if (pct) to.value = String(v);
    else to.value = String(Math.max(1, Math.round(which === 'w' ? v * src.height / src.width : v * src.width / src.height)));
  }
  refreshSettingsUI();
}

function onUnitChange() {
  const src = state.source;
  const pct = radioValue('unit') === '%';
  if (src) {
    const conv = (input, full) => {
      const v = intOf(input.value.trim());
      if (!input.value.trim() || !Number.isFinite(v) || v <= 0) return;
      input.value = pct ? String(clamp(Math.round(v / full * 100), 1, 99)) : String(Math.max(1, Math.round(full * v / 100)));
    };
    conv(el.w, src.width);
    conv(el.h, src.height);
  }
  refreshSettingsUI();
}

function onSquareChange() {
  const src = state.source;
  if (el.square.checked && src) {
    const pct = radioValue('unit') === '%';
    if (pct) { setRadio('unit', 'px'); }
    const s = Math.min(src.width, src.height);
    el.w.value = String(s);
    el.h.value = String(s);
    el.keepAspect.checked = false;
  }
  refreshSettingsUI();
}

function applyPreset(name) {
  const src = state.source;
  if (!src) return;
  setRadio('unit', 'px');
  el.keepAspect.checked = false;
  el.square.checked = false;
  const W = src.width, H = src.height;
  switch (name) {
    case 'w-25': el.w.value = Math.round(W * 0.75); el.h.value = ''; break;
    case 'w-50': el.w.value = Math.round(W * 0.5); el.h.value = ''; break;
    case 'h-30': el.w.value = ''; el.h.value = Math.round(H * 0.7); break;
    case 'square': el.square.checked = true; el.w.value = Math.min(W, H); el.h.value = Math.min(W, H); break;
    case 'w+20': el.w.value = Math.round(W * 1.2); el.h.value = ''; break;
    default: break;
  }
  refreshSettingsUI();
}

function showRunError(msg, title) {
  el.runErrorTitle.textContent = title || "Caire couldn't run with these settings";
  el.runErrorMsg.textContent = msg;
  el.runError.hidden = false;
}
function clearRunError() { el.runError.hidden = true; }

/* =========================================================================
   7. Stage geometry and rendering
   ========================================================================= */
let renderQueued = false;
function scheduleRender() {
  if (renderQueued) return;
  renderQueued = true;
  requestAnimationFrame(render);
}

function resizeStage() {
  const r = el.stage.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  state.stageSize = { w: r.width, h: r.height };
  state.dpr = dpr;
  el.canvas.width = Math.max(1, Math.round(r.width * dpr));
  el.canvas.height = Math.max(1, Math.round(r.height * dpr));
  scheduleRender();
}

// Fit a box (in image pixels) into the stage. The image is anchored to the
// top-left of the box so it visibly shrinks toward the ruler origin.
function fitGeom(boxW, boxH) {
  const { w: sw, h: sh } = state.stageSize;
  const aw = Math.max(40, sw - RULER - STAGE_PAD * 2);
  const ah = Math.max(40, sh - RULER - STAGE_PAD * 2);
  const scale = Math.min(aw / boxW, ah / boxH, 2);
  const ox = Math.round(RULER + STAGE_PAD + (aw - boxW * scale) / 2);
  const oy = Math.round(RULER + STAGE_PAD + (ah - boxH * scale) / 2);
  return { scale, ox, oy, boxW, boxH };
}

function niceStep(scale) {
  const steps = [5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 2500, 5000, 10000];
  for (const s of steps) if (s * scale >= 64) return s;
  return 20000;
}

function drawRulers(ctx, g, marks) {
  const t = tokens();
  const { w: sw, h: sh } = state.stageSize;
  ctx.save();
  ctx.fillStyle = t.ruler;
  ctx.fillRect(0, 0, sw, RULER);
  ctx.fillRect(0, 0, RULER, sh);
  ctx.fillStyle = t.line;
  ctx.fillRect(0, RULER - 1, sw, 1);
  ctx.fillRect(RULER - 1, 0, 1, sh);

  const step = niceStep(g.scale);
  const minor = step / 5;
  ctx.font = `10px ${t.mono}`;
  ctx.textBaseline = 'top';

  // Top ruler
  const maxX = (sw - g.ox) / g.scale;
  for (let u = 0, i = 0; u <= maxX; u += minor, i++) {
    const x = Math.round(g.ox + u * g.scale) + 0.5;
    if (x < RULER) continue;
    const major = i % 5 === 0;
    ctx.fillStyle = major ? t.muted : t.faint;
    ctx.fillRect(x - 0.5, major ? RULER - 9 : RULER - 5, 1, major ? 9 : 5);
    if (major) ctx.fillText(String(Math.round(u)), x + 3, 4);
  }
  // Left ruler (labels rotated)
  const maxY = (sh - g.oy) / g.scale;
  for (let u = 0, i = 0; u <= maxY; u += minor, i++) {
    const y = Math.round(g.oy + u * g.scale) + 0.5;
    if (y < RULER) continue;
    const major = i % 5 === 0;
    ctx.fillStyle = major ? t.muted : t.faint;
    ctx.fillRect(major ? RULER - 9 : RULER - 5, y - 0.5, major ? 9 : 5, 1);
    if (major) {
      ctx.save();
      ctx.translate(4, y + 3);
      ctx.rotate(Math.PI / 2);
      ctx.fillText(String(Math.round(u)), 0, -10);
      ctx.restore();
    }
  }

  // Markers: edges of the original, the target, and the live image.
  for (const m of marks) {
    const color = t[m.color] || m.color;
    const p = m.axis === 'x' ? Math.round(g.ox + m.at * g.scale) + 0.5 : Math.round(g.oy + m.at * g.scale) + 0.5;
    ctx.strokeStyle = color;
    ctx.fillStyle = color;
    ctx.lineWidth = 1;
    if (m.guide) {
      ctx.save();
      ctx.globalAlpha = 0.75;
      ctx.setLineDash(m.dash ? [4, 4] : []);
      ctx.beginPath();
      if (m.axis === 'x') { ctx.moveTo(p, RULER); ctx.lineTo(p, sh); } else { ctx.moveTo(RULER, p); ctx.lineTo(sw, p); }
      ctx.stroke();
      ctx.restore();
    }
    ctx.beginPath();
    if (m.axis === 'x') { ctx.moveTo(p - 5, RULER - 7); ctx.lineTo(p + 5, RULER - 7); ctx.lineTo(p, RULER - 1); }
    else { ctx.moveTo(RULER - 7, p - 5); ctx.lineTo(RULER - 7, p + 5); ctx.lineTo(RULER - 1, p); }
    ctx.closePath();
    ctx.fill();
    if (m.label) {
      ctx.font = `500 10px ${t.mono}`;
      const text = m.label;
      const tw = ctx.measureText(text).width + 8;
      if (m.axis === 'x') {
        const lx = clamp(p - tw / 2, RULER + 2, sw - tw - 2);
        ctx.fillRect(lx, 2, tw, 13);
        ctx.fillStyle = t.stage;
        ctx.fillText(text, lx + 4, 3.5);
      } else {
        ctx.save();
        ctx.translate(2, clamp(p - tw / 2, RULER + 2, sh - tw - 2));
        ctx.fillRect(0, 0, 13, tw);
        ctx.translate(13, 0);
        ctx.rotate(Math.PI / 2);
        ctx.fillStyle = t.stage;
        ctx.fillText(text, 4, 1.5);
        ctx.restore();
      }
    }
  }

  ctx.fillStyle = t.ruler;
  ctx.fillRect(0, 0, RULER - 1, RULER - 1);
  ctx.fillStyle = t.faint;
  ctx.font = `9px ${t.mono}`;
  ctx.fillText('px', 6, 8);
  ctx.restore();
}

function drawFrameBorder(ctx, x, y, w, h, color) {
  ctx.save();
  ctx.strokeStyle = color;
  ctx.globalAlpha = 0.6;
  ctx.lineWidth = 1;
  ctx.strokeRect(Math.round(x) - 0.5, Math.round(y) - 0.5, Math.round(w) + 1, Math.round(h) + 1);
  ctx.restore();
}

function chipWidth(ctx, text) {
  ctx.save();
  ctx.font = `500 11px ${tokens().font}`;
  const tw = ctx.measureText(text).width + 12;
  ctx.restore();
  return tw;
}

function drawChip(ctx, text, x, y, align = 'left') {
  const t = tokens();
  ctx.save();
  ctx.font = `500 11px ${t.font}`;
  const tw = ctx.measureText(text).width + 12;
  const lx = align === 'right' ? x - tw : x;
  ctx.globalAlpha = 0.85;
  ctx.fillStyle = t.stage;
  ctx.fillRect(lx, y, tw, 18);
  ctx.globalAlpha = 1;
  ctx.fillStyle = t.text;
  ctx.textBaseline = 'middle';
  ctx.fillText(text, lx + 6, y + 9.5);
  ctx.restore();
}

function render() {
  renderQueued = false;
  const ctx = el.canvas.getContext('2d');
  const { w: sw, h: sh } = state.stageSize;
  ctx.setTransform(state.dpr, 0, 0, state.dpr, 0, 0);
  ctx.clearRect(0, 0, sw, sh);
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = 'high';
  el.divider.hidden = true;

  if (state.view === 'result' && state.result) renderResult(ctx);
  else if (state.view === 'live' && state.job) renderLive(ctx);
  else if (state.source) renderSource(ctx);
  else state.geom = null;

  updateStageCursor();
}

function renderSource(ctx) {
  const s = state.source;
  const ui = readForm();
  const v = validate(ui, s);
  const t = v.ok ? estimateTarget(ui, s) : { w: s.width, h: s.height };
  const g = fitGeom(Math.max(s.width, t.w), Math.max(s.height, t.h));
  state.geom = g;
  const dw = s.width * g.scale, dh = s.height * g.scale;
  ctx.drawImage(s.bitmap, g.ox, g.oy, dw, dh);
  if (state.brush.show) {
    ctx.save();
    ctx.globalAlpha = 0.5;
    ctx.drawImage(s.protect, g.ox, g.oy, dw, dh);
    ctx.drawImage(s.remove, g.ox, g.oy, dw, dh);
    ctx.restore();
  }
  drawFrameBorder(ctx, g.ox, g.oy, dw, dh, tokens().lineStrong);
  const marks = [{ axis: 'x', at: s.width, color: 'muted' }, { axis: 'y', at: s.height, color: 'muted' }];
  if (v.ok && t.w !== s.width) marks.push({ axis: 'x', at: t.w, color: 'amber', guide: true, dash: true, label: String(t.w) });
  if (v.ok && t.h !== s.height) marks.push({ axis: 'y', at: t.h, color: 'amber', guide: true, dash: true, label: String(t.h) });
  drawRulers(ctx, g, marks);
}

function renderLive(ctx) {
  const job = state.job;
  const s = job.source;
  const g = fitGeom(job.box.w, job.box.h);
  state.geom = g;
  const tk = tokens();
  let curW = s.width, curH = s.height;

  if (job.frame) {
    const { img, meta, realW } = job.frame;
    const k = realW / img.naturalWidth;          // real px per jpeg px
    const dw = img.naturalWidth * k * g.scale;
    const dh = img.naturalHeight * k * g.scale;
    curW = Math.round(img.naturalWidth * k);
    curH = Math.round(img.naturalHeight * k);
    ctx.drawImage(img, g.ox, g.oy, dw, dh);
    drawFrameBorder(ctx, g.ox, g.oy, dw, dh, tk.cyan);
    if (job.api.debug && Array.isArray(meta.seams) && meta.seams.length) {
      drawSeam(ctx, meta.seams, g.ox, g.oy, dw / img.naturalWidth, job.ui.shape, job.ui.seamColor);
    }
  } else {
    ctx.save();
    ctx.globalAlpha = 0.35;
    ctx.drawImage(s.bitmap, g.ox, g.oy, s.width * g.scale, s.height * g.scale);
    ctx.restore();
    drawFrameBorder(ctx, g.ox, g.oy, s.width * g.scale, s.height * g.scale, tk.lineStrong);
  }

  const tw = job.target.w, th = job.target.h;
  const marks = [];
  if (tw !== s.width) marks.push({ axis: 'x', at: s.width, color: 'faint' });
  if (th !== s.height) marks.push({ axis: 'y', at: s.height, color: 'faint' });
  marks.push({ axis: 'x', at: tw, color: 'amber', guide: tw !== curW, dash: true, label: tw !== curW ? String(tw) : '' });
  marks.push({ axis: 'y', at: th, color: 'amber', guide: th !== curH, dash: true, label: th !== curH ? String(th) : '' });
  marks.push({ axis: 'x', at: curW, color: 'cyan', label: String(curW) });
  marks.push({ axis: 'y', at: curH, color: 'cyan', label: String(curH) });
  drawRulers(ctx, g, marks);
}

function drawSeam(ctx, pts, ox, oy, k, shape, color) {
  ctx.save();
  ctx.fillStyle = color;
  ctx.strokeStyle = color;
  if (shape === 'line') {
    ctx.lineWidth = 1.5;
    ctx.lineJoin = 'round';
    ctx.beginPath();
    pts.forEach((p, i) => {
      const x = ox + (p[0] + 0.5) * k, y = oy + (p[1] + 0.5) * k;
      if (i) ctx.lineTo(x, y); else ctx.moveTo(x, y);
    });
    ctx.stroke();
  } else {
    ctx.beginPath();
    for (const p of pts) {
      const x = ox + (p[0] + 0.5) * k, y = oy + (p[1] + 0.5) * k;
      ctx.moveTo(x + 1.5, y);
      ctx.arc(x, y, 1.5, 0, Math.PI * 2);
    }
    ctx.fill();
  }
  ctx.restore();
}

function renderResult(ctx) {
  const r = state.result;
  const s = r.source;
  const H = r.height;
  const oW = Math.max(1, Math.round(s.width * H / s.height)); // original at the same height
  const boxW = Math.max(oW, r.width);
  const g = fitGeom(boxW, H);
  state.geom = g;
  const tk = tokens();
  const dh = H * g.scale;
  const pick = state.compare.pick;
  const comp = pick === 'naive'
    ? { img: naiveOf(r), w: r.width, label: `Naive scale ${dims(r.width, r.height)}` }
    : { img: s.bitmap, w: oW, label: `Original, scaled to ${H} px tall` };
  const carved = { img: r.bitmap, w: r.width, label: `Seam carved ${dims(r.width, r.height)}` };

  if (state.compare.on) {
    const boxPx = boxW * g.scale;
    const divX = g.ox + clamp(state.compare.pos, 0, 1) * boxPx;
    ctx.save();
    ctx.beginPath(); ctx.rect(g.ox, g.oy, divX - g.ox, dh); ctx.clip();
    ctx.drawImage(comp.img, g.ox, g.oy, comp.w * g.scale, dh);
    ctx.restore();
    ctx.save();
    ctx.beginPath(); ctx.rect(divX, g.oy, g.ox + boxPx - divX, dh); ctx.clip();
    ctx.drawImage(carved.img, g.ox, g.oy, carved.w * g.scale, dh);
    ctx.restore();
    drawFrameBorder(ctx, g.ox, g.oy, carved.w * g.scale, dh, tk.cyan);
    // Show each label only when it fits entirely on its own side of the divider.
    const gap = 8;
    const carvedRight = g.ox + carved.w * g.scale;
    if (divX - g.ox >= chipWidth(ctx, comp.label) + gap * 2) drawChip(ctx, comp.label, g.ox + gap, g.oy + gap);
    if (carvedRight - divX >= chipWidth(ctx, carved.label) + gap * 2) drawChip(ctx, carved.label, carvedRight - gap, g.oy + gap, 'right');
    el.divider.hidden = false;
    el.divider.style.left = `${divX}px`;
    el.divider.style.top = `${g.oy}px`;
    el.divider.style.height = `${dh}px`;
    el.divider.setAttribute('aria-valuenow', String(Math.round(state.compare.pos * 100)));
    el.divider.setAttribute('aria-valuetext', `${Math.round(state.compare.pos * 100)}% ${comp.label.split(' ')[0].toLowerCase()}`);
  } else {
    const one = pick === 'carved' ? carved : comp;
    ctx.drawImage(one.img, g.ox, g.oy, one.w * g.scale, dh);
    drawFrameBorder(ctx, g.ox, g.oy, one.w * g.scale, dh, pick === 'carved' ? tk.cyan : tk.lineStrong);
    drawChip(ctx, one.label, g.ox + 8, g.oy + 8);
  }

  const marks = [{ axis: 'x', at: r.width, color: 'cyan', label: String(r.width) }, { axis: 'y', at: H, color: 'cyan', label: String(H) }];
  if (oW !== r.width) marks.push({ axis: 'x', at: oW, color: 'faint' });
  drawRulers(ctx, g, marks);
}

/* =========================================================================
   8. Mask painter
   ========================================================================= */
let painting = null;
let dragging = null;

function toImage(e) {
  const g = state.geom;
  const r = el.stage.getBoundingClientRect();
  const sx = e.clientX - r.left, sy = e.clientY - r.top;
  return { sx, sy, x: (sx - g.ox) / g.scale, y: (sy - g.oy) / g.scale };
}

function canPaint() { return state.view === 'source' && !!state.source && !!state.geom; }
function canCompare() { return state.view === 'result' && !!state.result && state.compare.on && !!state.geom; }

function strokeOn(canvas, a, b, width, erase, color) {
  const c = canvas.getContext('2d');
  c.save();
  c.globalCompositeOperation = erase ? 'destination-out' : 'source-over';
  c.strokeStyle = color;
  c.lineWidth = width;
  c.lineCap = 'round';
  c.lineJoin = 'round';
  c.beginPath();
  c.moveTo(a.x, a.y);
  c.lineTo(b.x + 0.001, b.y);
  c.stroke();
  c.restore();
}

function paintSegment(a, b) {
  const s = state.source;
  const width = state.brush.size / state.geom.scale;
  const t = tokens();
  switch (state.brush.mode) {
    case 'protect':
      strokeOn(s.protect, a, b, width, false, t.protect || '#3ddc84');
      strokeOn(s.remove, a, b, width, true, '#000');
      break;
    case 'remove':
      strokeOn(s.remove, a, b, width, false, t.remove || '#ff5d5d');
      strokeOn(s.protect, a, b, width, true, '#000');
      break;
    default:
      strokeOn(s.protect, a, b, width, true, '#000');
      strokeOn(s.remove, a, b, width, true, '#000');
  }
  scheduleRender();
}

function updateRing(e) {
  if (!canPaint() || !e) { el.ring.classList.remove('is-visible'); return; }
  const p = toImage(e);
  const g = state.geom, s = state.source;
  const inside = p.x >= -2 && p.y >= -2 && p.x <= s.width + 2 && p.y <= s.height + 2;
  el.ring.classList.toggle('is-visible', inside);
  el.ring.dataset.mode = state.brush.mode;
  el.ring.style.width = el.ring.style.height = `${state.brush.size}px`;
  el.ring.style.left = `${p.sx}px`;
  el.ring.style.top = `${p.sy}px`;
  void g;
}
let lastPointer = null;

function updateStageCursor() {
  el.stage.classList.toggle('is-painting', canPaint());
  el.stage.classList.toggle('is-comparing', canCompare());
  if (!canPaint()) el.ring.classList.remove('is-visible');
}

function setDividerFrom(e) {
  const g = state.geom;
  const r = el.stage.getBoundingClientRect();
  const x = e.clientX - r.left;
  state.compare.pos = clamp((x - g.ox) / (g.boxW * g.scale), 0, 1);
  scheduleRender();
}

function setupStagePointer() {
  const c = el.canvas;
  const startDrag = (e) => {
    if (e.button !== 0 && e.pointerType === 'mouse') return;
    if (canPaint()) {
      const p = toImage(e);
      painting = { id: e.pointerId, last: p };
      try { el.stage.setPointerCapture(e.pointerId); } catch (_) { /* synthetic or stale pointer */ }
      paintSegment(p, p);
      e.preventDefault();
    } else if (canCompare()) {
      dragging = { id: e.pointerId };
      try { el.stage.setPointerCapture(e.pointerId); } catch (_) { /* synthetic or stale pointer */ }
      setDividerFrom(e);
      e.preventDefault();
    }
  };
  c.addEventListener('pointerdown', startDrag);
  el.divider.addEventListener('pointerdown', (e) => {
    if (!canCompare()) return;
    dragging = { id: e.pointerId };
    try { el.stage.setPointerCapture(e.pointerId); } catch (_) { /* ignore */ }
    e.preventDefault();
  });
  el.stage.addEventListener('pointermove', (e) => {
    lastPointer = e;
    updateRing(e);
    if (painting && e.pointerId === painting.id && canPaint()) {
      const events = typeof e.getCoalescedEvents === 'function' ? e.getCoalescedEvents() : [e];
      for (const ev of (events.length ? events : [e])) {
        const p = toImage(ev);
        paintSegment(painting.last, p);
        painting.last = p;
      }
    } else if (dragging && e.pointerId === dragging.id && canCompare()) {
      setDividerFrom(e);
    }
  });
  const end = (e) => {
    if (painting && e.pointerId === painting.id) painting = null;
    if (dragging && e.pointerId === dragging.id) dragging = null;
  };
  el.stage.addEventListener('pointerup', end);
  el.stage.addEventListener('pointercancel', end);
  el.stage.addEventListener('pointerleave', () => { el.ring.classList.remove('is-visible'); lastPointer = null; });

  el.divider.addEventListener('keydown', (e) => {
    const step = e.shiftKey ? 0.1 : 0.02;
    if (e.key === 'ArrowLeft') state.compare.pos = clamp(state.compare.pos - step, 0, 1);
    else if (e.key === 'ArrowRight') state.compare.pos = clamp(state.compare.pos + step, 0, 1);
    else if (e.key === 'Home') state.compare.pos = 0;
    else if (e.key === 'End') state.compare.pos = 1;
    else return;
    e.preventDefault();
    scheduleRender();
  });
}

function setBrushSize(n) {
  state.brush.size = clamp(Math.round(n), 4, 160);
  el.brushSize.value = String(state.brush.size);
  el.brushOut.textContent = String(state.brush.size);
  store.set('caire.brush', state.brush.size);
  if (lastPointer) updateRing(lastPointer);
}

function clearMasks() {
  const s = state.source;
  if (!s) return;
  for (const c of [s.protect, s.remove]) c.getContext('2d').clearRect(0, 0, c.width, c.height);
  scheduleRender();
  toast('Masks cleared.');
}

function maskHasPaint(canvas) {
  const k = Math.min(1, 512 / Math.max(canvas.width, canvas.height));
  const w = Math.max(1, Math.round(canvas.width * k)), h = Math.max(1, Math.round(canvas.height * k));
  const probe = makeCanvas(w, h);
  const pc = probe.getContext('2d', { willReadFrequently: true });
  pc.drawImage(canvas, 0, 0, w, h);
  const data = pc.getImageData(0, 0, w, h).data;
  for (let i = 3; i < data.length; i += 4) if (data[i] > 0) return true;
  return false;
}

// White where painted, black elsewhere, at source resolution.
function maskToBlob(canvas) {
  if (!maskHasPaint(canvas)) return Promise.resolve(null);
  const { width: w, height: h } = canvas;
  const white = makeCanvas(w, h);
  const wc = white.getContext('2d');
  wc.drawImage(canvas, 0, 0);
  wc.globalCompositeOperation = 'source-in';
  wc.fillStyle = '#fff';
  wc.fillRect(0, 0, w, h);
  const out = makeCanvas(w, h);
  const oc = out.getContext('2d');
  oc.fillStyle = '#000';
  oc.fillRect(0, 0, w, h);
  oc.drawImage(white, 0, 0);
  return new Promise((resolve, reject) => {
    out.toBlob((b) => (b ? resolve(b) : reject(new Error('Mask export failed'))), 'image/png');
  });
}

/* =========================================================================
   9. Jobs and the live event stream
   ========================================================================= */
const ACTIVE = new Set(['uploading', 'connecting', 'running', 'finishing', 'cancelling']);
const isActive = (job) => !!job && ACTIVE.has(job.status);

function closeStream(job) {
  if (job && job.es) { job.es.close(); job.es = null; }
  if (job && job.lostTimer) { clearTimeout(job.lostTimer); job.lostTimer = 0; }
  if (job) job.reconnecting = false;
}

async function run() {
  const ui = readForm();
  const src = state.source;
  const v = validate(ui, src);
  clearRunError();
  if (!v.ok) {
    if (v.size) { el.sizeError.hidden = false; el.sizeError.textContent = v.size; el.w.focus(); }
    if (v.general) showRunError(v.general, 'Not ready yet');
    return;
  }

  const prev = state.job;
  if (prev) closeStream(prev);   // the server cancels it when the new job starts

  const target = estimateTarget(ui, src);
  const job = {
    token: ++jobSeq, id: null, es: null, ui, api: v.api, source: src,
    status: 'uploading', target,
    box: { w: Math.max(src.width, target.w), h: Math.max(src.height, target.h) },
    total: 0, done: 0, pass: '', elapsedMs: 0, cur: { w: src.width, h: src.height },
    frame: null, pending: null, decoding: false, cancelRequested: false, startedAt: performance.now(),
  };
  state.job = job;
  setTabEnabled('live', true);
  setView('live');
  updateTelemetry();

  const fd = new FormData();
  fd.append('image', src.blob, src.name);
  try {
    const [mask, rmask] = await Promise.all([maskToBlob(src.protect), maskToBlob(src.remove)]);
    if (mask) fd.append('mask', mask, 'mask.png');
    if (rmask) fd.append('rmask', rmask, 'rmask.png');
    job.masks = { protect: !!mask, remove: !!rmask };
  } catch (_) {
    return failJob(job, "Couldn't export the masks from the browser.");
  }
  if (state.job !== job) return; // replaced while the masks were exported
  fd.append('params', JSON.stringify(v.api));

  // Uploads go out one at a time: the server cancels whichever job started before,
  // so an older upload that finished parsing late would otherwise cancel this run.
  const send = postChain.then(() => fetch('/api/jobs', { method: 'POST', body: fd }));
  postChain = send.then(() => {}, () => {});
  let res;
  try { res = await send; }
  catch (_) { return failJob(job, "Couldn't reach the Caire Studio server. Check that it is still running.", 'Connection failed'); }
  if (state.job !== job) { cancelSuperseded(res); return; }
  if (!res.ok) {
    const msg = await readError(res);
    if (state.job !== job) return;
    return failJob(job, msg, res.status >= 500 ? 'The server hit a problem' : undefined, true);
  }
  let data;
  try { data = await res.json(); } catch (_) { return failJob(job, 'The server sent an unreadable reply.'); }
  if (state.job !== job) { if (data && data.id) postCancel(data.id); return; }
  job.id = data.id;
  if (job.cancelRequested) postCancel(job.id);
  else job.status = 'connecting';
  updateTelemetry();
  connect(job);
}

let postChain = Promise.resolve();

// A newer run replaced this one while its upload was in flight: stop it on the server.
function cancelSuperseded(res) {
  if (!res || !res.ok) return;
  res.json().then((d) => { if (d && d.id) postCancel(d.id); }).catch(() => {});
}

function failJob(job, msg, title, backToSource) {
  if (state.job !== job) return;
  closeStream(job);
  job.status = 'error';
  job.error = msg;
  showRunError(msg, title);
  updateTelemetry();
  if (backToSource && !job.frame) setView('source');
  else scheduleRender();
}

function connect(job) {
  const es = new EventSource(`/api/jobs/${encodeURIComponent(job.id)}/events`);
  job.es = es;
  es.addEventListener('open', () => connectionBack(job));
  const on = (name, fn) => es.addEventListener(name, (e) => {
    if (state.job !== job) return;
    connectionBack(job);
    let d = null;
    if (e.data) { try { d = JSON.parse(e.data); } catch (_) { return; } }
    fn(d, e);
  });

  on('start', (d) => {
    job.total = d.total | 0;
    job.target = { w: d.targetWidth, h: d.targetHeight };
    job.effective = { w: d.width, h: d.height };
    job.cur = { w: d.width, h: d.height };
    job.box = { w: Math.max(job.source.width, d.targetWidth, d.width), h: Math.max(job.source.height, d.targetHeight, d.height) };
    if (job.status !== 'cancelling') job.status = 'running';
    updateTelemetry();
    scheduleRender();
  });
  on('progress', (d) => {
    job.done = d.done | 0;
    job.total = d.total | 0 || job.total;
    job.cur = { w: d.width, h: d.height };
    job.pass = d.pass || job.pass;
    job.elapsedMs = d.elapsedMs | 0;
    if (job.status === 'connecting') job.status = 'running';
    updateTelemetry();
  });
  on('frame', (d) => {
    job.pending = d;
    if (d.pass) job.pass = d.pass;
    if (!job.decoding) pumpFrames(job);
  });
  on('done', (d) => {
    closeStream(job);
    job.elapsedMs = d.elapsedMs | 0;
    job.cur = { w: d.width, h: d.height };
    job.done = job.total;
    job.status = 'finishing';
    updateTelemetry();
    finalize(job, d);
  });
  on('error', (d) => {
    if (d && d.error) {
      closeStream(job);
      if (/cancel/i.test(d.error)) {
        job.status = 'cancelled';
        updateTelemetry();
        scheduleRender();
      } else {
        failJob(job, d.error, 'Caire stopped with an error');
      }
      return;
    }
    // Native connection error: EventSource retries forever while CONNECTING (for
    // example when the server was stopped), so give up after LOST_AFTER_MS.
    if (!isActive(job)) return;
    if (es.readyState === EventSource.CLOSED) {
      failJob(job, 'Lost the live connection to the server.', 'Connection lost');
    } else if (!job.lostTimer) {
      job.reconnecting = true;
      updateTelemetry();
      job.lostTimer = setTimeout(() => {
        job.lostTimer = 0;
        if (state.job === job && isActive(job) && job.reconnecting) {
          failJob(job, "Lost the live connection to the server. Check that Caire Studio is still running, then run again.", 'Connection lost');
        }
      }, LOST_AFTER_MS);
    }
  });
}

function connectionBack(job) {
  if (job.lostTimer) { clearTimeout(job.lostTimer); job.lostTimer = 0; }
  if (job.reconnecting) {
    job.reconnecting = false;
    if (state.job === job) updateTelemetry();
  }
}

async function pumpFrames(job) {
  job.decoding = true;
  while (job.pending && state.job === job) {
    const d = job.pending;
    job.pending = null;
    const img = new Image();
    img.src = `data:image/jpeg;base64,${d.jpeg}`;
    try { await img.decode(); } catch (_) { continue; }
    if (state.job !== job) break;
    // frame.width may be the real size or the downscaled jpeg size. When it
    // matches the jpeg, use the latest progress size if its aspect agrees.
    const natW = img.naturalWidth, natH = img.naturalHeight;
    let realW = natW;
    if (d.fullWidth > 0) realW = d.fullWidth;
    else if (d.width > natW + 1) realW = d.width;
    else if (job.cur && job.cur.w > 0 && job.cur.h > 0 && Math.abs(job.cur.w / job.cur.h - natW / natH) < 0.03 * (natW / natH) + 0.01) realW = job.cur.w;
    job.frame = { img, meta: d, realW };
    if (state.view === 'live') scheduleRender();
  }
  job.decoding = false;
}

async function finalize(job, d) {
  let bitmap = null;
  try {
    const res = await fetch(`/api/jobs/${encodeURIComponent(job.id)}/result?format=png`);
    if (!res.ok) throw new Error(await readError(res));
    bitmap = await createImageBitmap(await res.blob());
  } catch (_) {
    if (job.frame) {
      try { bitmap = await createImageBitmap(job.frame.img); } catch (__) { bitmap = null; }
    }
  }
  if (state.job !== job) return;
  if (!bitmap) return failJob(job, "The run finished but the result couldn't be downloaded.");

  const result = {
    id: job.id, source: job.source, ui: job.ui, api: job.api, masks: job.masks, bitmap, naive: null,
    width: bitmap.width, height: bitmap.height, elapsedMs: d.elapsedMs | 0 || job.elapsedMs,
  };
  job.status = 'done';
  state.result = result;
  addHistory(result);
  setTabEnabled('result', true);
  updateTelemetry();
  setView('result');
}

function postCancel(id) {
  fetch(`/api/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }).catch(() => {});
}

function cancel() {
  const job = state.job;
  if (!isActive(job) || job.status === 'finishing') return;
  job.status = 'cancelling';
  job.cancelRequested = true;
  updateTelemetry();
  if (job.id) postCancel(job.id);
}

function updateTelemetry() {
  const job = state.job;
  const show = (node, on) => { node.hidden = !on; };
  const statusEl = el.statusText;
  statusEl.classList.remove('is-error');
  el.progress.classList.remove('is-done');

  el.cancelBtn.disabled = !isActive(job) || job.status === 'finishing' || job.status === 'cancelling';
  el.runLabel.textContent = isActive(job) ? 'Restart run' : 'Carve image';

  if (!job) {
    statusEl.textContent = state.source ? 'Ready. Press Carve image, or Ctrl+Enter.' : 'Load an image to begin.';
    [el.roSeams, el.roPass, el.roSize, el.roElapsed, el.roRate].forEach((n) => show(n, false));
    el.progressFill.style.width = '0%';
    el.progress.setAttribute('aria-valuenow', '0');
    return;
  }

  const pct = job.total ? clamp(job.done / job.total * 100, 0, 100) : 0;
  el.progressFill.style.width = `${job.status === 'done' ? 100 : pct}%`;
  el.progress.setAttribute('aria-valuenow', String(Math.round(job.status === 'done' ? 100 : pct)));

  const labels = {
    uploading: 'Uploading image…',
    connecting: 'Waiting for Caire to start…',
    running: 'Carving',
    finishing: 'Fetching the full-resolution result…',
    cancelling: 'Cancelling…',
    cancelled: 'Cancelled. The last frame is still on the stage.',
    done: `Done in ${fmtTime(job.elapsedMs)}.`,
    error: job.error || 'Something went wrong.',
  };
  statusEl.textContent = job.reconnecting && isActive(job)
    ? 'Reconnecting to the server…'
    : labels[job.status] || '';
  if (job.status === 'error') statusEl.classList.add('is-error');
  if (job.status === 'done') el.progress.classList.add('is-done');

  const hasNumbers = job.total > 0;
  show(el.roSeams, hasNumbers);
  el.roDone.textContent = String(job.done);
  el.roTotal.textContent = String(job.total);
  show(el.roPass, !!job.pass && job.status !== 'done');
  el.roPass.textContent = job.pass === 'vertical' ? 'Vertical pass, carving rows' : 'Horizontal pass, carving columns';
  show(el.roSize, hasNumbers);
  el.roSize.textContent = `${dims(job.cur.w, job.cur.h)} px`;
  show(el.roElapsed, job.elapsedMs > 0);
  el.roElapsed.textContent = fmtTime(job.elapsedMs);
  const rate = job.elapsedMs > 0 ? job.done / (job.elapsedMs / 1000) : 0;
  show(el.roRate, rate > 0);
  el.roRate.textContent = `${rate >= 100 ? Math.round(rate) : rate.toFixed(1)} seams/s`;
}

/* =========================================================================
   10. Views and result tools
   ========================================================================= */
function setTabEnabled(view, on) {
  const tab = el.tabs.find((t) => t.dataset.view === view);
  if (tab) tab.disabled = !on;
}

function setView(view) {
  if (view === 'live' && !state.job) view = 'source';
  if (view === 'result' && !state.result) view = 'source';
  state.view = view;
  for (const t of el.tabs) t.setAttribute('aria-selected', String(t.dataset.view === view));
  el.maskTools.hidden = view !== 'source' || !state.source;
  el.resultTools.hidden = view !== 'result';
  if (view === 'result') syncDownload();
  highlightHistory();
  scheduleRender();
}

function syncDownload() {
  const r = state.result;
  if (!r) return;
  const fmt = radioValue('format') || 'png';
  el.downloadBtn.href = `/api/jobs/${encodeURIComponent(r.id)}/result?format=${fmt}`;
  el.downloadBtn.setAttribute('download', `caire-${r.width}x${r.height}.${fmt}`);
  el.downloadBtn.textContent = `Download ${fmt === 'jpg' ? 'JPEG' : 'PNG'}`;
}

function saveBlob(blob, name) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60000);
}

// Download from the server, or encode the result held in the browser when the
// server no longer has it (it keeps a limited number of results).
async function downloadResult(e) {
  const r = state.result;
  if (!r) return;
  e.preventDefault();
  const fmt = radioValue('format') || 'png';
  const name = `caire-${r.width}x${r.height}.${fmt}`;
  try {
    const res = await fetch(el.downloadBtn.href);
    if (res.ok) { saveBlob(await res.blob(), name); return; }
  } catch (_) { /* fall back to the local copy */ }
  if (!r.bitmap) { toast("This result is no longer available. Run it again to download it.", 'error'); return; }
  const c = makeCanvas(r.width, r.height);
  c.getContext('2d').drawImage(r.bitmap, 0, 0);
  c.toBlob((b) => {
    if (b) saveBlob(b, name);
    else toast("Couldn't encode the result in the browser.", 'error');
  }, fmt === 'jpg' ? 'image/jpeg' : 'image/png', 0.95);
}

/* =========================================================================
   11. History
   ========================================================================= */
function summarize(ui, api, masks) {
  const parts = [];
  const u = ui.unit === '%' ? '%' : '';
  if (api.square) parts.push('square');
  else {
    if (api.width) parts.push(`w ${api.width}${u}`);
    if (api.height) parts.push(`h ${api.height}${u}`);
  }
  if (api.blur !== 4) parts.push(`blur ${api.blur}`);
  if (api.sobel !== 2) parts.push(`sobel ${api.sobel}`);
  if (api.face) parts.push(api.faceAngle ? `faces ${api.faceAngle}°` : 'faces');
  if (masks && masks.protect && masks.remove) parts.push('protect + remove masks');
  else if (masks && masks.protect) parts.push('protect mask');
  else if (masks && masks.remove) parts.push('remove mask');
  if (api.debug) parts.push('seams shown');
  return parts.join(', ');
}

// The naive (plain scale) comparison image is built only when it is shown.
function naiveOf(r) {
  if (!r.naive && r.source.bitmap) {
    r.naive = makeCanvas(r.width, r.height);
    const nc = r.naive.getContext('2d');
    nc.imageSmoothingEnabled = true;
    nc.imageSmoothingQuality = 'high';
    nc.drawImage(r.source.bitmap, 0, 0, r.width, r.height);
  }
  return r.naive;
}

function releaseResult(h) {
  if (h.bitmap) { if (h.bitmap.close) h.bitmap.close(); h.bitmap = null; }
  if (h.naive) { h.naive.width = 0; h.naive.height = 0; h.naive = null; }
}

// Keep full-resolution pixels only for the newest few results (and the one on the
// stage); older entries keep their thumbnail and are fetched again when restored.
// Source bitmaps no entry needs are closed too; they can be decoded again from the blob.
function compactHistory(evicted = []) {
  state.history.forEach((h, i) => {
    if (i >= FULL_RESULTS && h !== state.result) releaseResult(h);
  });
  evicted.forEach((h) => { if (h !== state.result) releaseResult(h); });
  const needed = new Set([state.source, state.job && state.job.source, state.result && state.result.source]);
  state.history.forEach((h) => { if (h.bitmap) needed.add(h.source); });
  for (const h of [...state.history, ...evicted]) {
    const s = h.source;
    if (!needed.has(s) && s.bitmap) { if (s.bitmap.close) s.bitmap.close(); s.bitmap = null; }
  }
}

async function hydrate(h) {
  if (!h.source.bitmap) h.source.bitmap = await decodeImage(h.source.blob);
  if (!h.bitmap) {
    const res = await fetch(`/api/jobs/${encodeURIComponent(h.id)}/result?format=png`);
    if (!res.ok) throw new Error(res.status === 404 ? 'gone' : await readError(res));
    h.bitmap = await createImageBitmap(await res.blob());
  }
}

function addHistory(result) {
  const th = 60;
  const tw = Math.max(1, Math.round(result.width * th / result.height));
  const c = makeCanvas(Math.min(tw, 220), th);
  c.getContext('2d').drawImage(result.bitmap, 0, 0, c.width, c.height);
  let thumb = '';
  try { thumb = c.toDataURL('image/jpeg', 0.8); } catch (_) { thumb = ''; }
  result.thumb = thumb;
  result.summary = summarize(result.ui, result.api, result.masks);
  state.history.unshift(result);
  const evicted = state.history.splice(20); // the server keeps at most 20 results too
  compactHistory(evicted);
  store.set('caire.history', state.history.map((h) => ({ ui: h.ui, width: h.width, height: h.height, source: h.source.name })));
  renderHistory();
}

function renderHistory() {
  el.historyStrip.innerHTML = '';
  if (!state.history.length) {
    const p = document.createElement('span');
    p.className = 'hint history-empty';
    p.textContent = 'Finished runs appear here. Click one to bring back its settings and result.';
    el.historyStrip.append(p);
    return;
  }
  for (const h of state.history) {
    const item = document.createElement('div');
    item.className = 'list-item';
    item.setAttribute('role', 'listitem');
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'run';
    b.dataset.id = h.id;
    b.setAttribute('aria-label', `Run ${dims(h.width, h.height)}, ${h.summary}, from ${h.source.name}`);
    const img = document.createElement('img');
    img.alt = '';
    if (h.thumb) img.src = h.thumb;
    const meta = document.createElement('span');
    meta.className = 'run-meta';
    const d = document.createElement('span'); d.className = 'run-dims'; d.textContent = dims(h.width, h.height);
    const s = document.createElement('span'); s.className = 'run-sum'; s.textContent = h.summary; s.title = `${h.summary} (${h.source.name})`;
    const t = document.createElement('span'); t.className = 'run-time'; t.textContent = fmtTime(h.elapsedMs);
    meta.append(d, s, t);
    b.append(img, meta);
    b.addEventListener('click', () => restoreHistory(h));
    item.append(b);
    el.historyStrip.append(item);
  }
  highlightHistory();
}

function highlightHistory() {
  const id = state.view === 'result' && state.result ? state.result.id : null;
  $$('.run', el.historyStrip).forEach((b) => b.setAttribute('aria-current', String(b.dataset.id === id)));
}

async function restoreHistory(h) {
  try {
    await hydrate(h);
  } catch (err) {
    if (err && err.message === 'gone') {
      releaseResult(h);
      state.history = state.history.filter((x) => x !== h);
      renderHistory();
      toast('That run is no longer on the server, so it was removed from the history.', 'error');
    } else {
      toast("Couldn't bring back that run. Check that Caire Studio is still running.", 'error');
    }
    return;
  }
  if (state.source !== h.source) {
    state.source = h.source;
    onSourceChanged();
  }
  applyForm(h.ui);
  state.result = h;
  compactHistory();
  setTabEnabled('result', true);
  setView('result');
}

/* =========================================================================
   12. Keyboard
   ========================================================================= */
function isTyping(target) {
  if (!target) return false;
  const tag = target.tagName;
  if (tag === 'TEXTAREA' || target.isContentEditable) return true;
  if (tag === 'INPUT') return !['checkbox', 'radio', 'range', 'button', 'color', 'file'].includes(target.type);
  return false;
}

function setupKeyboard() {
  document.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      if (state.source) run();
      return;
    }
    if (e.key === 'Escape') {
      if (isActive(state.job)) { e.preventDefault(); cancel(); }
      return;
    }
    if (isTyping(e.target) || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === '[') { e.preventDefault(); setBrushSize(state.brush.size - (state.brush.size > 40 ? 8 : 4)); }
    else if (e.key === ']') { e.preventDefault(); setBrushSize(state.brush.size + (state.brush.size >= 40 ? 8 : 4)); }
  });
}

/* =========================================================================
   13. Init
   ========================================================================= */
function bind() {
  el.fileInputs.forEach((i) => i && i.addEventListener('change', () => pickFile(i)));
  el.themeToggle.addEventListener('click', toggleTheme);
  if (lightQuery) {
    const onChange = () => { tokenCache = null; syncThemeButton(); scheduleRender(); };
    if (lightQuery.addEventListener) lightQuery.addEventListener('change', onChange);
    else if (lightQuery.addListener) lightQuery.addListener(onChange);
  }
  el.loadSampleBtn.addEventListener('click', () => { const s = defaultSample(); if (s) loadSample(s); });

  el.w.addEventListener('input', () => onSizeInput('w'));
  el.h.addEventListener('input', () => onSizeInput('h'));
  $$('input[name="unit"]').forEach((r) => r.addEventListener('change', onUnitChange));
  el.square.addEventListener('change', onSquareChange);
  el.keepAspect.addEventListener('change', () => { if (el.keepAspect.checked && el.w.value) onSizeInput('w'); else refreshSettingsUI(); });
  $$('[data-preset]').forEach((b) => b.addEventListener('click', () => applyPreset(b.dataset.preset)));
  [el.blur, el.sobel, el.faceAngle, el.face, el.debug, el.seamColor].forEach((i) => i.addEventListener('input', refreshSettingsUI));
  [el.face, el.debug].forEach((i) => i.addEventListener('change', refreshSettingsUI));
  $$('input[name="shape"]').forEach((r) => r.addEventListener('change', refreshSettingsUI));
  $$('input[name="format"]').forEach((r) => r.addEventListener('change', () => { refreshSettingsUI(); syncDownload(); }));
  el.form.addEventListener('submit', (e) => { e.preventDefault(); run(); });

  el.runBtn.addEventListener('click', run);
  el.cancelBtn.addEventListener('click', cancel);

  el.tabs.forEach((t) => t.addEventListener('click', () => { if (!t.disabled) setView(t.dataset.view); }));
  $('.tabs').addEventListener('keydown', (e) => {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    const enabled = el.tabs.filter((t) => !t.disabled);
    const i = enabled.indexOf(document.activeElement);
    if (i < 0) return;
    const next = enabled[(i + (e.key === 'ArrowRight' ? 1 : enabled.length - 1)) % enabled.length];
    next.focus();
    setView(next.dataset.view);
    e.preventDefault();
  });

  $$('input[name="brush"]').forEach((r) => r.addEventListener('change', () => {
    state.brush.mode = radioValue('brush') || 'protect';
    if (lastPointer) updateRing(lastPointer);
  }));
  el.brushSize.addEventListener('input', () => setBrushSize(Number(el.brushSize.value)));
  el.showMasks.addEventListener('change', () => { state.brush.show = el.showMasks.checked; scheduleRender(); });
  el.clearMasks.addEventListener('click', clearMasks);

  $$('input[name="rview"]').forEach((r) => r.addEventListener('change', () => {
    state.compare.pick = radioValue('rview') || 'carved';
    scheduleRender();
  }));
  el.compareOn.addEventListener('change', () => { state.compare.on = el.compareOn.checked; scheduleRender(); });
  el.downloadBtn.addEventListener('click', downloadResult);

  setupStagePointer();
  setupDropAndPaste();
  setupKeyboard();

  if (typeof ResizeObserver === 'function') new ResizeObserver(resizeStage).observe(el.stage);
  else window.addEventListener('resize', resizeStage);
  window.addEventListener('resize', () => { tokenCache = null; });
}

function init() {
  syncThemeButton();
  applyForm(store.get('caire.params', DEFAULTS) || DEFAULTS);
  setBrushSize(Number(store.get('caire.brush', 32)) || 32);
  bind();
  resizeStage();
  updateTelemetry();
  setView('source');
  loadSamples();
  // Fonts load late; redraw ruler labels once they arrive.
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(() => scheduleRender()).catch(() => {});
}

init();
