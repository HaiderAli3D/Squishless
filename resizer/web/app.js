(() => {
  'use strict';

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  const MAX_BYTES = 30 * 1024 * 1024;
  const MAX_PIXELS = 50e6;
  const PARALLEL = 2;
  const ACCEPT = /^image\/(jpeg|png|webp|bmp|gif|x-ms-bmp)$/;

  const MODE_HINTS = {
    exact: 'Each image is reshaped to exactly this size.',
    percent: 'Each image is scaled relative to its own size. Use different values to change the shape.',
    width: 'Only the width changes. The height stays as it is.',
    height: 'Only the height changes. The width stays as it is.',
  };

  // ---------- Settings ----------
  const ui = {
    tabs: $$('.tabs [role=tab]'),
    panel: $('#mode-fields'),
    hint: $('#mode-hint'),
    w: $('#in-w'), h: $('#in-h'), pw: $('#in-pw'), ph: $('#in-ph'),
    faces: $('#opt-faces'), format: $('#opt-format'),
    quality: $('#opt-quality'), qualityOut: $('#quality-out'), qualityField: $('#quality-field'),
    run: $('#btn-run'), zip: $('#btn-zip'), clear: $('#btn-clear'),
    drop: $('#drop'), fileInput: $('#file-input'), list: $('#list'), summary: $('#summary'),
    tpl: $('#row-tpl'), toast: $('#toast'),
  };

  const settings = { mode: 'exact', w: 1920, h: 1080, pw: 75, ph: 100, faces: true, format: 'same', quality: 92 };
  try {
    const saved = JSON.parse(localStorage.getItem('caire-resize-settings') || 'null');
    if (saved && typeof saved === 'object') Object.assign(settings, saved);
  } catch (_) { /* storage unavailable */ }

  function saveSettings() {
    try { localStorage.setItem('caire-resize-settings', JSON.stringify(settings)); } catch (_) { /* ignore */ }
  }

  function applySettingsToUI() {
    ui.w.value = settings.w || '';
    ui.h.value = settings.h || '';
    ui.pw.value = settings.pw;
    ui.ph.value = settings.ph;
    ui.faces.checked = !!settings.faces;
    ui.format.value = settings.format;
    ui.quality.value = settings.quality;
    ui.qualityOut.textContent = settings.quality;
    setMode(settings.mode, false);
    updateQualityState();
    markPreset();
  }

  function setMode(mode, focus) {
    if (!MODE_HINTS[mode]) mode = 'exact';
    settings.mode = mode;
    for (const t of ui.tabs) {
      const on = t.dataset.mode === mode;
      t.setAttribute('aria-selected', String(on));
      t.tabIndex = on ? 0 : -1;
      if (on) {
        ui.panel.setAttribute('aria-labelledby', t.id);
        if (focus) t.focus();
      }
    }
    for (const el of $$('[data-show]', ui.panel)) {
      el.hidden = !el.dataset.show.split(' ').includes(mode);
    }
    ui.hint.textContent = MODE_HINTS[mode];
  }

  function readInt(input) {
    const v = input.value.trim();
    if (v === '') return 0;
    const n = Number(v);
    return Number.isFinite(n) ? Math.round(n) : NaN;
  }

  function onSettingsInput() {
    settings.w = readInt(ui.w);
    settings.h = readInt(ui.h);
    settings.pw = readInt(ui.pw);
    settings.ph = readInt(ui.ph);
    settings.faces = ui.faces.checked;
    settings.format = ui.format.value;
    settings.quality = Number(ui.quality.value);
    ui.qualityOut.textContent = settings.quality;
    validateInputs();
    updateQualityState();
    markPreset();
    saveSettings();
    refreshAll();
  }

  function validateInputs() {
    const bad = (input, ok) => input.parentElement.classList.toggle('invalid', !ok);
    bad(ui.w, Number.isInteger(settings.w) && settings.w >= 1 || (settings.mode === 'height'));
    bad(ui.h, Number.isInteger(settings.h) && settings.h >= 1 || (settings.mode === 'width'));
    bad(ui.pw, settings.pw >= 10 && settings.pw <= 400);
    bad(ui.ph, settings.ph >= 10 && settings.ph <= 400);
  }

  function updateQualityState() {
    const off = settings.format === 'png';
    ui.qualityField.classList.toggle('disabled', off);
    ui.quality.disabled = off;
  }

  function markPreset() {
    for (const b of $$('[data-preset]')) {
      const [m, a, c] = b.dataset.preset.split(':');
      const n1 = Number(a), n2 = Number(c);
      const on = m === settings.mode && (m === 'exact'
        ? settings.w === n1 && settings.h === n2
        : settings.pw === n1 && settings.ph === n2);
      b.setAttribute('aria-pressed', String(on));
    }
  }

  ui.tabs.forEach((t, i) => {
    t.addEventListener('click', () => { setMode(t.dataset.mode, false); onSettingsInput(); });
    t.addEventListener('keydown', (e) => {
      let j = -1;
      if (e.key === 'ArrowRight') j = (i + 1) % ui.tabs.length;
      else if (e.key === 'ArrowLeft') j = (i - 1 + ui.tabs.length) % ui.tabs.length;
      else if (e.key === 'Home') j = 0;
      else if (e.key === 'End') j = ui.tabs.length - 1;
      if (j < 0) return;
      e.preventDefault();
      setMode(ui.tabs[j].dataset.mode, true);
      onSettingsInput();
    });
  });
  for (const el of [ui.w, ui.h, ui.pw, ui.ph, ui.quality]) el.addEventListener('input', onSettingsInput);
  for (const el of [ui.faces, ui.format]) el.addEventListener('change', onSettingsInput);
  for (const b of $$('[data-preset]')) {
    b.addEventListener('click', () => {
      const [m, a, c] = b.dataset.preset.split(':');
      if (m === 'exact') { ui.w.value = a; ui.h.value = c; } else { ui.pw.value = a; ui.ph.value = c; }
      setMode(m, false);
      onSettingsInput();
    });
  }

  // ---------- Items ----------
  /** @type {Array<any>} */
  let items = [];
  let nextId = 1;
  let running = false;

  function targetFor(it) {
    const s = settings;
    let w, h;
    switch (s.mode) {
      case 'percent':
        if (!(s.pw >= 10 && s.pw <= 400) || !(s.ph >= 10 && s.ph <= 400)) return { error: 'Percent must be 10 to 400' };
        w = Math.max(1, Math.round(it.w * s.pw / 100));
        h = Math.max(1, Math.round(it.h * s.ph / 100));
        break;
      case 'width': w = s.w; h = it.h; break;
      case 'height': w = it.w; h = s.h; break;
      default: w = s.w; h = s.h;
    }
    if (!Number.isInteger(w) || !Number.isInteger(h) || w < 1 || h < 1) return { error: 'Enter a size' };
    if (w > it.w * 4 || h > it.h * 4) return { w, h, error: 'More than 4x the original' };
    if (w * h > MAX_PIXELS) return { w, h, error: 'Result over 50 megapixels' };
    if (w === it.w && h === it.h) return { w, h, error: 'Already this size' };
    return { w, h };
  }

  function jobKey(it, t) {
    return [t.w, t.h, settings.faces, settings.format, settings.format === 'png' ? 0 : settings.quality, it.maskVersion].join('|');
  }

  function fmtSize(w, h) { return `${w} × ${h}`; }
  function fmtTime(ms) { return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)} s`; }
  function fmtBytes(b) { return b < 1024 * 1024 ? `${Math.max(1, Math.round(b / 1024))} KB` : `${(b / 1048576).toFixed(1)} MB`; }

  async function readDims(file) {
    if ('createImageBitmap' in window) {
      let bmp = null;
      try { bmp = await createImageBitmap(file, { imageOrientation: 'none' }); }
      catch (_) { try { bmp = await createImageBitmap(file); } catch (_) { bmp = null; } }
      if (bmp) { const d = { w: bmp.width, h: bmp.height }; bmp.close(); return d; }
    }
    return new Promise((resolve, reject) => {
      const img = new Image();
      const url = URL.createObjectURL(file);
      img.onload = () => { URL.revokeObjectURL(url); resolve({ w: img.naturalWidth, h: img.naturalHeight }); };
      img.onerror = () => { URL.revokeObjectURL(url); reject(new Error('not an image')); };
      img.src = url;
    });
  }

  async function addFiles(files) {
    const list = Array.from(files || []).filter((f) => f && (ACCEPT.test(f.type) || /\.(jpe?g|png|webp|bmp|gif)$/i.test(f.name)));
    if (!list.length) { toast('Those files are not images this app can read (JPG, PNG, WebP, BMP, GIF).'); return; }
    for (const file of list) {
      const it = {
        id: nextId++, file, name: file.name || `pasted-${nextId}.png`, w: 0, h: 0,
        url: URL.createObjectURL(file), status: 'loading', error: '', result: null,
        resultKey: '', paint: null, masks: { protect: null, remove: null }, maskVersion: 0, ctrl: null,
      };
      items.push(it);
      buildRow(it);
      refreshRow(it);
      try {
        const d = await readDims(file);
        it.w = d.w; it.h = d.h;
        if (file.size > MAX_BYTES) setError(it, 'Larger than 30 MB');
        else if (it.w * it.h > MAX_PIXELS) setError(it, 'More than 50 megapixels');
        else it.status = 'ready';
      } catch (_) {
        setError(it, 'Could not read this image');
      }
      refreshRow(it);
    }
    refreshChrome();
  }

  function setError(it, msg) { it.status = 'error'; it.error = msg; it.fatal = true; }

  function buildRow(it) {
    const li = ui.tpl.content.firstElementChild.cloneNode(true);
    it.el = li;
    const img = $('.thumb img', li);
    img.src = it.url;
    $('.name', li).textContent = it.name;
    $('.name', li).title = it.name;
    $('.thumb', li).addEventListener('click', () => { if (it.result) openCompare(it); });
    $('.areas', li).addEventListener('click', () => openMasker(it));
    $('.remove', li).addEventListener('click', () => removeItem(it));
    $('.remove', li).setAttribute('aria-label', `Remove ${it.name}`);
    $('.areas', li).setAttribute('aria-label', `Mark areas to keep or remove in ${it.name}`);
    ui.list.appendChild(li);
  }

  function refreshRow(it) {
    const li = it.el;
    if (!li) return;
    const t = it.w ? targetFor(it) : null;
    $('.orig', li).textContent = it.w ? fmtSize(it.w, it.h) : 'Reading…';
    const tgt = $('.target', li);
    const arrow = $('.arrow', li);
    if (t && t.w) {
      tgt.textContent = fmtSize(t.w, t.h);
      tgt.classList.toggle('bad', !!t.error && t.error !== 'Already this size');
      arrow.hidden = false;
    } else { tgt.textContent = ''; arrow.hidden = true; }

    const stale = it.result && t && !t.error && jobKey(it, t) !== it.resultKey;
    let status = '', cls = '';
    switch (it.status) {
      case 'loading': status = 'Reading…'; break;
      case 'ready': status = t && t.error ? t.error : 'Ready'; break;
      case 'queued': status = 'Waiting'; break;
      case 'working': status = 'Resizing…'; break;
      case 'done':
        cls = 'done';
        status = stale
          ? 'Settings changed, resize again'
          : `Done in ${fmtTime(it.result.ms)}, ${fmtSize(it.result.w, it.result.h)}, ${fmtBytes(it.result.blob.size)}`;
        break;
      case 'error': cls = 'error'; status = it.error; break;
    }
    li.className = 'item' + (cls ? ' ' + cls : '');
    $('.status', li).textContent = status;

    const tags = $('.tags', li);
    tags.textContent = '';
    if (it.masks.protect || it.masks.remove) {
      const s = document.createElement('span');
      s.className = 'tag';
      s.textContent = it.masks.protect && it.masks.remove ? 'Keep and remove areas' : it.masks.protect ? 'Keep areas' : 'Remove areas';
      tags.appendChild(s);
    }

    const bar = $('.bar', li);
    bar.hidden = !(it.status === 'queued' || it.status === 'working');
    bar.classList.toggle('waiting', it.status === 'queued');

    const dl = $('.dl', li);
    dl.hidden = !it.result;
    if (it.result) {
      dl.href = it.result.url;
      dl.download = it.result.filename;
      dl.setAttribute('aria-label', `Download ${it.result.filename}`);
    }
    const thumb = $('.thumb', li);
    thumb.disabled = !it.result;
    thumb.setAttribute('aria-label', it.result ? `Compare ${it.name} before and after` : it.name);
    $('.areas', li).disabled = !it.w || it.fatal || it.status === 'working' || it.status === 'queued';
  }

  function needsWork(it) {
    if (it.fatal || !it.w) return false;
    if (it.status === 'queued' || it.status === 'working') return false;
    const t = targetFor(it);
    if (t.error) return false;
    return !(it.result && it.resultKey === jobKey(it, t));
  }

  function refreshAll() { items.forEach(refreshRow); refreshChrome(); }

  function refreshChrome() {
    const n = items.length;
    const done = items.filter((i) => i.result).length;
    const todo = items.filter(needsWork).length;
    ui.drop.classList.toggle('compact', n > 0);
    ui.clear.hidden = n === 0 || running;
    ui.run.disabled = running || todo === 0;
    ui.run.textContent = running ? 'Resizing…' : todo && todo < n ? `Resize ${todo} image${todo > 1 ? 's' : ''}` : 'Resize all';
    ui.zip.disabled = done === 0;
    ui.zip.textContent = done > 1 ? `Download all (${done}, .zip)` : 'Download all (.zip)';
    ui.summary.textContent = n === 0 ? '' : `${n} image${n > 1 ? 's' : ''}${done ? `, ${done} resized` : ''}`;
  }

  function removeItem(it) {
    if (it.ctrl) it.ctrl.abort();
    URL.revokeObjectURL(it.url);
    if (it.result) URL.revokeObjectURL(it.result.url);
    it.el.remove();
    items = items.filter((x) => x !== it);
    refreshChrome();
  }

  ui.clear.addEventListener('click', () => { [...items].forEach(removeItem); });

  // ---------- Processing ----------
  async function runAll() {
    if (running) return;
    const queue = items.filter(needsWork);
    if (!queue.length) return;
    running = true;
    for (const it of queue) { it.status = 'queued'; it.error = ''; refreshRow(it); }
    refreshChrome();
    const worker = async () => {
      while (queue.length) {
        const it = queue.shift();
        if (!items.includes(it)) continue;
        await processItem(it);
      }
    };
    await Promise.all(Array.from({ length: PARALLEL }, worker));
    running = false;
    refreshAll();
    const failed = items.filter((i) => i.status === 'error' && !i.fatal).length;
    if (failed) toast(`${failed} image${failed > 1 ? 's' : ''} could not be resized.`);
  }

  async function processItem(it) {
    const t = targetFor(it);
    if (t.error) { it.status = it.result ? 'done' : 'ready'; refreshRow(it); return; }
    const key = jobKey(it, t);
    it.status = 'working';
    refreshRow(it);
    const fd = new FormData();
    fd.append('image', it.file, it.name);
    fd.append('width', String(t.w === it.w ? 0 : t.w));
    fd.append('height', String(t.h === it.h ? 0 : t.h));
    fd.append('faces', settings.faces ? '1' : '0');
    fd.append('format', settings.format);
    fd.append('quality', String(settings.quality));
    if (it.masks.protect) fd.append('protect', it.masks.protect, 'protect.png');
    if (it.masks.remove) fd.append('remove', it.masks.remove, 'remove.png');
    it.ctrl = new AbortController();
    try {
      const res = await fetch('/api/resize', { method: 'POST', body: fd, signal: it.ctrl.signal });
      if (!res.ok) {
        let msg = `Server error (${res.status})`;
        try { const j = await res.json(); if (j && j.error) msg = j.error; } catch (_) { /* not json */ }
        throw new Error(msg);
      }
      const blob = await res.blob();
      const w = Number(res.headers.get('X-Width')) || t.w;
      const h = Number(res.headers.get('X-Height')) || t.h;
      const ms = Number(res.headers.get('X-Elapsed-Ms')) || 0;
      const filename = filenameFrom(res.headers.get('Content-Disposition')) || defaultName(it, w, h, blob.type);
      if (it.result) URL.revokeObjectURL(it.result.url);
      it.result = { blob, url: URL.createObjectURL(blob), w, h, ms, filename };
      it.resultKey = key;
      it.status = 'done';
    } catch (err) {
      if (err && err.name === 'AbortError') return;
      it.status = 'error';
      it.error = (err && err.message) || 'Resize failed';
      if (err instanceof TypeError) it.error = 'Could not reach the app. Is it still running?';
    } finally {
      it.ctrl = null;
      if (items.includes(it)) refreshRow(it);
      refreshChrome();
    }
  }

  function filenameFrom(cd) {
    if (!cd) return '';
    const star = /filename\*=UTF-8''([^;]+)/i.exec(cd);
    if (star) { try { return decodeURIComponent(star[1]); } catch (_) { /* fall through */ } }
    const plain = /filename="([^"]*)"/i.exec(cd);
    return plain ? plain[1] : '';
  }

  function defaultName(it, w, h, type) {
    const base = it.name.replace(/\.[^.]+$/, '') || 'image';
    return `${base}-${w}x${h}.${type === 'image/jpeg' ? 'jpg' : 'png'}`;
  }

  ui.run.addEventListener('click', runAll);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && !document.querySelector('dialog[open]')) {
      e.preventDefault();
      runAll();
    }
  });

  // ---------- Input: picker, drop, paste ----------
  ui.fileInput.addEventListener('change', () => { addFiles(ui.fileInput.files); ui.fileInput.value = ''; });
  let dragDepth = 0;
  document.addEventListener('dragenter', (e) => {
    if (!e.dataTransfer || !Array.from(e.dataTransfer.types).includes('Files')) return;
    dragDepth++; ui.drop.classList.add('over');
  });
  document.addEventListener('dragleave', () => { if (--dragDepth <= 0) { dragDepth = 0; ui.drop.classList.remove('over'); } });
  document.addEventListener('dragover', (e) => { if (e.dataTransfer && Array.from(e.dataTransfer.types).includes('Files')) e.preventDefault(); });
  document.addEventListener('drop', (e) => {
    if (!e.dataTransfer || !e.dataTransfer.files.length) return;
    e.preventDefault();
    dragDepth = 0; ui.drop.classList.remove('over');
    addFiles(e.dataTransfer.files);
  });
  document.addEventListener('paste', (e) => {
    const files = Array.from((e.clipboardData && e.clipboardData.files) || []).filter((f) => f.type.startsWith('image/'));
    if (!files.length) return;
    e.preventDefault();
    addFiles(files.map((f, i) => (f.name && f.name !== 'image.png') ? f
      : new File([f], `pasted-${Date.now()}${files.length > 1 ? '-' + (i + 1) : ''}.${f.type === 'image/jpeg' ? 'jpg' : 'png'}`, { type: f.type })));
  });

  // ---------- ZIP (store only) ----------
  const CRC_TABLE = (() => {
    const t = new Uint32Array(256);
    for (let n = 0; n < 256; n++) {
      let c = n;
      for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
      t[n] = c >>> 0;
    }
    return t;
  })();

  function crc32(bytes) {
    let c = 0xffffffff;
    for (let i = 0; i < bytes.length; i++) c = CRC_TABLE[(c ^ bytes[i]) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  }

  async function buildZip(entries) {
    const enc = new TextEncoder();
    const now = new Date();
    const dosTime = (now.getHours() << 11) | (now.getMinutes() << 5) | (now.getSeconds() >> 1);
    const dosDate = ((now.getFullYear() - 1980) << 9) | ((now.getMonth() + 1) << 5) | now.getDate();
    const parts = [];
    const central = [];
    let offset = 0;
    for (const e of entries) {
      const data = new Uint8Array(await e.blob.arrayBuffer());
      const name = enc.encode(e.name);
      const crc = crc32(data);
      const lh = new DataView(new ArrayBuffer(30));
      lh.setUint32(0, 0x04034b50, true);
      lh.setUint16(4, 20, true);
      lh.setUint16(6, 0x0800, true); // UTF-8 names
      lh.setUint16(8, 0, true); // store
      lh.setUint16(10, dosTime, true);
      lh.setUint16(12, dosDate, true);
      lh.setUint32(14, crc, true);
      lh.setUint32(18, data.length, true);
      lh.setUint32(22, data.length, true);
      lh.setUint16(26, name.length, true);
      lh.setUint16(28, 0, true);
      parts.push(lh.buffer, name, data);

      const ch = new DataView(new ArrayBuffer(46));
      ch.setUint32(0, 0x02014b50, true);
      ch.setUint16(4, 20, true);
      ch.setUint16(6, 20, true);
      ch.setUint16(8, 0x0800, true);
      ch.setUint16(10, 0, true);
      ch.setUint16(12, dosTime, true);
      ch.setUint16(14, dosDate, true);
      ch.setUint32(16, crc, true);
      ch.setUint32(20, data.length, true);
      ch.setUint32(24, data.length, true);
      ch.setUint16(28, name.length, true);
      // extra, comment, disk, internal attrs, external attrs = 0
      ch.setUint32(42, offset, true);
      central.push(ch.buffer, name);
      offset += 30 + name.length + data.length;
    }
    const cdSize = central.reduce((s, p) => s + p.byteLength, 0);
    const end = new DataView(new ArrayBuffer(22));
    end.setUint32(0, 0x06054b50, true);
    end.setUint16(8, entries.length, true);
    end.setUint16(10, entries.length, true);
    end.setUint32(12, cdSize, true);
    end.setUint32(16, offset, true);
    return new Blob([...parts, ...central, end.buffer], { type: 'application/zip' });
  }

  ui.zip.addEventListener('click', async () => {
    const done = items.filter((i) => i.result);
    if (!done.length) return;
    ui.zip.disabled = true;
    try {
      const used = new Set();
      const entries = done.map((it) => {
        let name = it.result.filename;
        const dot = name.lastIndexOf('.');
        for (let n = 2; used.has(name.toLowerCase()); n++) name = `${it.result.filename.slice(0, dot)} (${n})${it.result.filename.slice(dot)}`;
        used.add(name.toLowerCase());
        return { name, blob: it.result.blob };
      });
      const zip = await buildZip(entries);
      const url = URL.createObjectURL(zip);
      const a = document.createElement('a');
      a.href = url;
      a.download = `resized-${entries.length}-images.zip`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 30000);
    } catch (err) {
      toast('Could not build the ZIP file.');
    } finally {
      refreshChrome();
    }
  });

  // ---------- Dialog helpers ----------
  for (const d of $$('dialog')) {
    d.addEventListener('click', (e) => {
      if (e.target === d) d.close(); // backdrop click
      if (e.target.closest('[data-close]')) d.close();
    });
  }

  // ---------- Compare ----------
  const cmp = {
    dlg: $('#compare'), title: $('#compare-title'), sub: $('#compare-sub'), body: $('#compare-body'),
    stage: $('#stage'), before: $('#cmp-before'), after: $('#cmp-after'), clip: $('#after-clip'),
    divider: $('#divider'), range: $('#cmp-range'), dl: $('#cmp-dl'), item: null,
  };

  function openCompare(it) {
    cmp.item = it;
    cmp.title.textContent = it.name;
    cmp.sub.textContent = `Original ${fmtSize(it.w, it.h)}, resized ${fmtSize(it.result.w, it.result.h)} in ${fmtTime(it.result.ms)}`;
    cmp.before.src = it.url;
    cmp.after.src = it.result.url;
    cmp.dl.href = it.result.url;
    cmp.dl.download = it.result.filename;
    cmp.range.value = 50;
    cmp.dlg.showModal();
    layoutCompare();
    setSplit(50);
  }

  function layoutCompare() {
    const it = cmp.item;
    if (!it || !cmp.dlg.open) return;
    const r = it.result;
    const maxW = Math.max(it.w, r.w), maxH = Math.max(it.h, r.h);
    const availW = cmp.body.clientWidth - 32;
    const availH = Math.max(160, window.innerHeight - 260);
    const s = Math.min(availW / maxW, availH / maxH, 2);
    const place = (img, w, h) => {
      img.style.width = `${w * s}px`;
      img.style.height = `${h * s}px`;
      img.style.left = `${(maxW - w) * s / 2}px`;
      img.style.top = `${(maxH - h) * s / 2}px`;
    };
    cmp.stage.style.width = `${maxW * s}px`;
    cmp.stage.style.height = `${maxH * s}px`;
    place(cmp.before, it.w, it.h);
    place(cmp.after, r.w, r.h);
  }

  function setSplit(p) {
    p = Math.max(0, Math.min(100, p));
    cmp.clip.style.clipPath = `inset(0 ${100 - p}% 0 0)`;
    cmp.divider.style.left = `${p}%`;
    cmp.range.value = String(Math.round(p));
  }

  cmp.range.addEventListener('input', () => setSplit(Number(cmp.range.value)));
  cmp.stage.addEventListener('pointerdown', (e) => {
    cmp.stage.setPointerCapture(e.pointerId);
    const move = (ev) => {
      const rect = cmp.stage.getBoundingClientRect();
      setSplit(((ev.clientX - rect.left) / rect.width) * 100);
    };
    move(e);
    const up = () => {
      cmp.stage.removeEventListener('pointermove', move);
      cmp.stage.removeEventListener('pointerup', up);
      cmp.stage.removeEventListener('pointercancel', up);
    };
    cmp.stage.addEventListener('pointermove', move);
    cmp.stage.addEventListener('pointerup', up);
    cmp.stage.addEventListener('pointercancel', up);
  });
  window.addEventListener('resize', () => { layoutCompare(); layoutMasker(); });

  // ---------- Mask editor ----------
  const mk = {
    dlg: $('#masker'), title: $('#mask-title'), body: $('#mask-body'), stage: $('#mask-stage'),
    img: $('#mask-img'), paint: $('#mask-paint'), cursor: $('#brush-cursor'), size: $('#brush-size'),
    tools: $$('#masker [data-tool]'), item: null, tool: 'protect', bitmap: null, dirty: false,
  };
  const COLORS = { protect: '#2ba864', remove: '#e0473c' };

  async function loadBitmap(file) {
    try { return await createImageBitmap(file, { imageOrientation: 'none' }); }
    catch (_) { return createImageBitmap(file); }
  }

  async function openMasker(it) {
    if (!it.w) return;
    let bmp;
    try { bmp = await loadBitmap(it.file); } catch (_) { toast('Could not open this image for editing.'); return; }
    if (bmp.width !== it.w || bmp.height !== it.h) { it.w = bmp.width; it.h = bmp.height; }
    mk.item = it;
    mk.bitmap = bmp;
    mk.dirty = false;
    mk.title.textContent = `Mark areas in ${it.name}`;
    mk.paint.width = it.w;
    mk.paint.height = it.h;
    const ctx = mk.paint.getContext('2d');
    ctx.clearRect(0, 0, it.w, it.h);
    if (it.paint) ctx.drawImage(it.paint, 0, 0);
    mk.dlg.showModal();
    layoutMasker();
  }

  function layoutMasker() {
    const it = mk.item;
    if (!it || !mk.dlg.open || !mk.bitmap) return;
    const availW = mk.body.clientWidth - 32;
    const availH = Math.max(160, window.innerHeight - 270);
    const s = Math.min(availW / it.w, availH / it.h, 1);
    const dw = Math.max(1, Math.round(it.w * s)), dh = Math.max(1, Math.round(it.h * s));
    const dpr = window.devicePixelRatio || 1;
    mk.stage.style.width = `${dw}px`;
    mk.stage.style.height = `${dh}px`;
    mk.img.width = Math.round(dw * dpr);
    mk.img.height = Math.round(dh * dpr);
    mk.img.style.width = `${dw}px`;
    mk.img.style.height = `${dh}px`;
    const c = mk.img.getContext('2d');
    c.imageSmoothingQuality = 'high';
    c.drawImage(mk.bitmap, 0, 0, mk.img.width, mk.img.height);
    updateCursorSize();
  }

  function setTool(tool) {
    mk.tool = tool;
    for (const b of mk.tools) b.setAttribute('aria-checked', String(b.dataset.tool === tool));
  }
  mk.tools.forEach((b, i) => {
    b.addEventListener('click', () => setTool(b.dataset.tool));
    b.addEventListener('keydown', (e) => {
      if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
      e.preventDefault();
      const j = (i + (e.key === 'ArrowRight' ? 1 : mk.tools.length - 1)) % mk.tools.length;
      setTool(mk.tools[j].dataset.tool);
      mk.tools[j].focus();
    });
  });

  function updateCursorSize() {
    const d = Number(mk.size.value);
    mk.cursor.style.width = mk.cursor.style.height = `${d}px`;
  }
  mk.size.addEventListener('input', updateCursorSize);

  function toSource(e) {
    const rect = mk.paint.getBoundingClientRect();
    const k = mk.item.w / rect.width;
    return { x: (e.clientX - rect.left) * k, y: (e.clientY - rect.top) * k, k };
  }

  function stroke(ctx, a, b) {
    ctx.save();
    ctx.globalCompositeOperation = mk.tool === 'erase' ? 'destination-out' : 'source-over';
    ctx.strokeStyle = ctx.fillStyle = mk.tool === 'erase' ? '#000' : COLORS[mk.tool];
    ctx.lineWidth = Number(mk.size.value) * a.k;
    ctx.lineCap = ctx.lineJoin = 'round';
    ctx.beginPath();
    if (a === b) ctx.arc(a.x, a.y, ctx.lineWidth / 2, 0, Math.PI * 2), ctx.fill();
    else { ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke(); }
    ctx.restore();
  }

  function moveCursor(e) {
    const rect = mk.stage.getBoundingClientRect();
    mk.cursor.style.left = `${e.clientX - rect.left}px`;
    mk.cursor.style.top = `${e.clientY - rect.top}px`;
  }

  mk.stage.addEventListener('pointerenter', () => { mk.cursor.hidden = false; });
  mk.stage.addEventListener('pointerleave', () => { mk.cursor.hidden = true; });
  mk.stage.addEventListener('pointermove', moveCursor);
  mk.stage.addEventListener('pointerdown', (e) => {
    if (!mk.item || e.button > 0) return;
    e.preventDefault();
    mk.stage.setPointerCapture(e.pointerId);
    const ctx = mk.paint.getContext('2d');
    let last = toSource(e);
    stroke(ctx, last, last);
    mk.dirty = true;
    const move = (ev) => {
      const evs = ev.getCoalescedEvents ? ev.getCoalescedEvents() : [ev];
      for (const ce of (evs.length ? evs : [ev])) {
        const p = toSource(ce);
        stroke(ctx, last, p);
        last = p;
      }
    };
    const up = () => {
      mk.stage.removeEventListener('pointermove', move);
      mk.stage.removeEventListener('pointerup', up);
      mk.stage.removeEventListener('pointercancel', up);
    };
    mk.stage.addEventListener('pointermove', move);
    mk.stage.addEventListener('pointerup', up);
    mk.stage.addEventListener('pointercancel', up);
  });

  $('#mask-clear').addEventListener('click', () => {
    if (!mk.item) return;
    mk.paint.getContext('2d').clearRect(0, 0, mk.paint.width, mk.paint.height);
    mk.dirty = true;
  });

  mk.dlg.addEventListener('close', () => {
    if (mk.bitmap) { mk.bitmap.close(); mk.bitmap = null; }
    mk.paint.width = mk.paint.height = 1; // release memory
    mk.item = null;
  });

  function canvasToBlob(c) {
    return new Promise((resolve, reject) => c.toBlob((b) => (b ? resolve(b) : reject(new Error('export failed'))), 'image/png'));
  }

  async function exportMasks(paint, w, h) {
    const src = paint.getContext('2d').getImageData(0, 0, w, h).data;
    const out = document.createElement('canvas');
    out.width = w; out.height = h;
    const octx = out.getContext('2d');
    const buf = octx.createImageData(w, h);
    const dst = buf.data;
    const one = async (pick) => {
      let any = false;
      for (let i = 0; i < src.length; i += 4) {
        const on = src[i + 3] > 64 && pick(src[i], src[i + 1]);
        const v = on ? 255 : 0;
        if (on) any = true;
        dst[i] = dst[i + 1] = dst[i + 2] = v;
        dst[i + 3] = 255;
      }
      if (!any) return null;
      octx.putImageData(buf, 0, 0);
      return canvasToBlob(out);
    };
    const protect = await one((r, g) => g > r);
    const remove = await one((r, g) => r > g);
    return { protect, remove };
  }

  $('#mask-save').addEventListener('click', async () => {
    const it = mk.item;
    if (!it) return;
    if (mk.dirty) {
      const btn = $('#mask-save');
      btn.disabled = true;
      try {
        // Copy the strokes before any await: closing the dialog meanwhile resets mk.paint.
        const paint = document.createElement('canvas');
        paint.width = it.w; paint.height = it.h;
        paint.getContext('2d').drawImage(mk.paint, 0, 0);
        const masks = await exportMasks(paint, it.w, it.h);
        it.paint = masks.protect || masks.remove ? paint : null;
        it.masks = masks;
        it.maskVersion++;
      } catch (_) {
        toast('Could not save the marked areas.');
      } finally {
        btn.disabled = false;
      }
    }
    mk.dlg.close();
    refreshRow(it);
    refreshChrome();
  });

  // ---------- Toast ----------
  let toastTimer = 0;
  function toast(msg) {
    ui.toast.textContent = msg;
    ui.toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { ui.toast.hidden = true; }, 4000);
  }

  applySettingsToUI();
  validateInputs();
  refreshChrome();
})();
