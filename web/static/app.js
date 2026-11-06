// Link2Doc front-end: a small hash-routed app with no build step.
import { marked } from '/vendor/marked.esm.js';

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const app = $('#app');
let ME = null; let FEAT = {}; let pollTimer = null;

async function api(path, opts = {}) {
  const res = await fetch(path, {
    ...opts,
    headers: opts.body && !(opts.body instanceof FormData) ? { 'content-type': 'application/json', ...(opts.headers || {}) } : opts.headers,
    body: opts.body && !(opts.body instanceof FormData) && typeof opts.body !== 'string' ? JSON.stringify(opts.body) : opts.body
  });
  const data = await res.json().catch(() => ({}));
  if (res.status === 401) { location.hash = '#/login'; throw new Error(data.error || 'กรุณาเข้าสู่ระบบ'); }
  if (!res.ok) throw new Error(data.error || data.detail || `ผิดพลาด (${res.status})`);
  return data;
}

function toast(msg, ms = 3500) {
  const t = $('#toast'); t.textContent = msg; t.hidden = false;
  clearTimeout(toast.t); toast.t = setTimeout(() => { t.hidden = true; }, ms);
}
const fail = (e) => toast(e.message || String(e), 6000);
const fmtDate = (d) => d ? new Date(d).toLocaleString('th-TH', { dateStyle: 'medium', timeStyle: 'short' }) : '—';
const num = (n) => Number(n || 0).toLocaleString('th-TH');

const ICON = {
  arrow: '<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M3 8h10m0 0L9 4m4 4l-4 4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  ext: '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M6 3h7v7M13 3L4 12" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>',
  link: '<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M6.5 9.5l3-3M5 7L3.5 8.5a2.5 2.5 0 003.5 3.5L8.5 10.5M11 9l1.5-1.5A2.5 2.5 0 009 4L7.5 5.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
  pdf: '<svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M4 1.5h5.5L12.5 4.5v10h-8.5z" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><path d="M6 9h4M6 11.5h3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
  upload: '<svg width="36" height="36" viewBox="0 0 24 24" fill="none" aria-hidden="true" style="color:var(--accent)"><path d="M12 16V4m0 0l-4 4m4-4l4 4M4 16v3a1 1 0 001 1h14a1 1 0 001-1v-3" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  check: '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M3 8.5l3 3 7-7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>'
};

const STATUS = {
  queued: ['รอคิว', ''], fetching: ['กำลังดึง', 'run'], ready: ['พร้อม', 'ok'], failed: ['ล้มเหลว', 'bad'],
  skipped: ['ข้าม', ''], written: ['ส่งแล้ว', 'ok']
};
const JOBSTATUS = { fetching: ['กำลังดึง', 'run'], review: ['รอตรวจ', 'run'], publishing: ['กำลังส่ง', 'run'], done: ['สำเร็จ', 'ok'], partial: ['บางส่วน', 'run'], failed: ['ล้มเหลว', 'bad'] };
const chip = (map, s) => { const [t, k] = map[s] || [s, '']; return `<span class="chip ${k}">${esc(t)}</span>`; };
const FREQ = { hourly: 'ทุกชั่วโมง', daily: 'ทุกวัน', weekly: 'ทุกสัปดาห์', monthly: 'ทุกเดือน' };
const DAYS = [['mon', 'จันทร์'], ['tue', 'อังคาร'], ['wed', 'พุธ'], ['thu', 'พฤหัสบดี'], ['fri', 'ศุกร์'], ['sat', 'เสาร์'], ['sun', 'อาทิตย์']];

function steps(active) {
  const names = ['ใส่ลิงก์', 'ดึงข้อมูล', 'ตรวจ Markdown', 'ส่งเข้า Google Doc'];
  return `<ol class="steps" aria-label="ขั้นตอน">${names.map((n, i) => {
    const cls = i + 1 < active ? 'done' : i + 1 === active ? 'on' : '';
    return `<li class="${cls}"><b>${i + 1 < active ? ICON.check : i + 1}</b>${n}</li>`;
  }).join('')}</ol>`;
}

function download(name, text) {
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob([text], { type: 'text/markdown;charset=utf-8' }));
  a.download = name.replace(/[\\/:*?"<>|]+/g, '_') + '.md';
  a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

// ---------- Google Picker (optional) ----------
let pickerReady;
function loadPicker() {
  if (!pickerReady) pickerReady = new Promise((resolve, reject) => {
    const s = document.createElement('script'); s.src = 'https://apis.google.com/js/api.js';
    s.onload = () => window.gapi.load('picker', resolve); s.onerror = reject; document.head.appendChild(s);
  });
  return pickerReady;
}
async function pick(kind) {
  const cfg = await api('/api/google/picker');
  await loadPicker();
  const g = window.google.picker;
  const view = kind === 'folder'
    ? new g.DocsView(g.ViewId.FOLDERS).setSelectFolderEnabled(true).setIncludeFolders(true).setMimeTypes('application/vnd.google-apps.folder')
    : new g.DocsView(g.ViewId.DOCUMENTS);
  view.setEnableDrives?.(true);
  return new Promise((resolve) => {
    new g.PickerBuilder().setOAuthToken(cfg.token).setDeveloperKey(cfg.apiKey).setAppId(cfg.appId)
      .enableFeature(g.Feature.SUPPORT_DRIVES).addView(view)
      .setCallback(d => { if (d.action === g.Action.PICKED) resolve(d.docs[0]); if (d.action === g.Action.CANCEL) resolve(null); })
      .build().setVisible(true);
  });
}

// ---------- Shared form parts ----------
function scheduleBlock(prefix = 'sc') {
  return `<section class="card" aria-labelledby="${prefix}-h">
    <div class="row between"><h2 id="${prefix}-h">ดึงซ้ำอัตโนมัติ</h2><span class="small muted">ปรับรายลิงก์ได้ในขั้นตรวจ Markdown</span></div>
    <div class="seg" role="radiogroup" aria-label="ความถี่" id="${prefix}-freq" style="max-width:640px">
      ${[['once', 'ครั้งเดียว'], ['daily', 'ทุกวัน'], ['weekly', 'ทุกสัปดาห์'], ['monthly', 'ทุกเดือน']].map(([v, t], i) => `<button type="button" role="radio" data-v="${v}" class="${i === 0 ? 'on' : ''}" aria-checked="${i === 0}">${t}</button>`).join('')}
    </div>
    <div id="${prefix}-more" class="cols" hidden>
      <div class="row" style="flex:1 1 320px">
        <label class="field" id="${prefix}-dayf" hidden>วัน<select id="${prefix}-day">${DAYS.map(([v, t]) => `<option value="${v}">${t}</option>`).join('')}</select></label>
        <label class="field" id="${prefix}-datef" hidden>วันที่<select id="${prefix}-date">${[1, 5, 10, 15, 20, 25].map(d => `<option>${d}</option>`).join('')}<option value="last">วันสุดท้ายของเดือน</option></select></label>
        <label class="field">เวลา<input type="time" id="${prefix}-time" value="07:00" style="min-height:44px;border:1px solid var(--line2);border-radius:10px;padding:0 10px;font:inherit"></label>
        <label class="field">สิ้นสุด<select id="${prefix}-end"><option value="">ไม่มีกำหนด</option><option value="30">หลัง 30 วัน</option><option value="90">หลัง 90 วัน</option></select></label>
      </div>
      <fieldset style="flex:1 1 340px"><legend>เมื่อเนื้อหาเปลี่ยน</legend>
        <label class="check"><input type="radio" name="${prefix}-mode" value="replace" checked>แทนที่เนื้อหาในแท็บเดิม</label>
        <label class="check"><input type="radio" name="${prefix}-mode" value="new_tab">เพิ่มแท็บใหม่ต่อท้ายชื่อด้วยวันที่ (เก็บฉบับเก่า)</label>
        <p class="small muted">ถ้าเนื้อหาเหมือนเดิม ระบบจะไม่แตะ Doc</p>
      </fieldset>
    </div>
  </section>`;
}
function bindSchedule(prefix = 'sc') {
  const seg = $(`#${prefix}-freq`);
  seg.addEventListener('click', (e) => {
    const b = e.target.closest('button'); if (!b) return;
    $$('button', seg).forEach(x => { x.classList.toggle('on', x === b); x.setAttribute('aria-checked', x === b); });
    const v = b.dataset.v;
    $(`#${prefix}-more`).hidden = v === 'once';
    $(`#${prefix}-dayf`).hidden = v !== 'weekly';
    $(`#${prefix}-datef`).hidden = v !== 'monthly';
  });
}
function readSchedule(prefix = 'sc') {
  const v = $(`#${prefix}-freq .on`).dataset.v;
  if (v === 'once') return null;
  const end = $(`#${prefix}-end`).value;
  return {
    frequency: v, time: $(`#${prefix}-time`).value || '07:00', day: $(`#${prefix}-day`).value, date: $(`#${prefix}-date`).value,
    updateMode: $(`input[name=${prefix}-mode]:checked`).value,
    endsAt: end ? new Date(Date.now() + Number(end) * 86400000).toISOString() : null
  };
}
async function difyOptions(sel) {
  try {
    const r = await api('/api/dify/datasets');
    const s = await api('/api/settings');
    if (!r.connected) { sel.innerHTML = '<option value="">ยังไม่ได้เชื่อม Dify (ไปที่ตั้งค่า)</option>'; return false; }
    sel.innerHTML = r.data.map(d => `<option value="${esc(d.id)}" ${s.dify?.default_dataset_id === d.id ? 'selected' : ''}>${esc(d.name)} (${num(d.documents)} เอกสาร)</option>`).join('');
    return true;
  } catch (e) { sel.innerHTML = `<option value="">${esc(e.message)}</option>`; return false; }
}
function sourceSwitch(active) {
  return `<nav class="seg" aria-label="แหล่งข้อมูล" style="align-self:flex-start;grid-auto-columns:minmax(140px,1fr)">
    <a href="#/new" class="${active === 'url' ? 'on' : ''}" ${active === 'url' ? 'aria-current="page"' : ''}>${ICON.link}ลิงก์เว็บ</a>
    <a href="#/pdf" class="${active === 'pdf' ? 'on' : ''}" ${active === 'pdf' ? 'aria-current="page"' : ''}>${ICON.pdf}ไฟล์ PDF</a></nav>`;
}
function destBlock({ dify = false } = {}) {
  const picker = FEAT.picker;
  return `<aside class="card side" aria-labelledby="dest-h">
    <h2 id="dest-h">ปลายทาง</h2>
    <label class="check"><input type="checkbox" id="toGoogle" ${dify ? '' : 'checked'}><b>Google Doc</b></label>
    <div id="gbox" class="col" style="display:flex;flex-direction:column;gap:10px" ${dify ? 'hidden' : ''}>
      <label class="option"><input type="radio" name="target" value="new" checked><span>สร้าง Doc ใหม่</span></label>
      <label class="field">ชื่อ Doc<input type="text" id="docTitle" value="เว็บ→MD ${new Date().toLocaleDateString('th-TH')}"></label>
      <label class="option"><input type="radio" name="target" value="existing"><span>เพิ่มแท็บใน Doc เดิม</span></label>
      <div class="row" id="existingRow" hidden><input type="url" id="docId" class="code" placeholder="วางลิงก์ Google Doc" style="flex:1"> ${picker ? '<button type="button" class="btn" data-pick="doc">เลือก</button>' : ''}</div>
      <label class="field" for="folder">โฟลเดอร์ใน Google Drive (รวม Shared drive)</label>
      <div class="row"><input type="url" id="folder" class="code" placeholder="วางลิงก์โฟลเดอร์ หรือเว้นว่างเพื่อใช้ค่าเริ่มต้น" style="flex:1;min-width:0">${picker ? '<button type="button" class="btn" data-pick="folder">เลือกจาก Drive</button>' : '<button type="button" class="btn" id="checkFolder">ตรวจ</button>'}</div>
      <span class="small muted" id="folderNote"></span>
      <fieldset><legend>วางเนื้อหาแบบ</legend>
        <div class="seg" role="radiogroup" id="layout">
          <button type="button" role="radio" data-v="tab" class="on" aria-checked="true">แท็บละลิงก์</button>
          <button type="button" role="radio" data-v="h1" aria-checked="false">หัวข้อละลิงก์</button>
          <button type="button" role="radio" data-v="doc" aria-checked="false">Doc ละลิงก์</button>
        </div>
      </fieldset>
      <fieldset><legend>รูปแบบใน Doc</legend>
        <label class="check"><input type="radio" name="format" value="formatted" checked>จัดรูปแบบ (หัวข้อ ตัวหนา ลิสต์)</label>
        <label class="check"><input type="radio" name="format" value="raw">Markdown ดิบ</label>
      </fieldset>
    </div>
    <label class="check"><input type="checkbox" id="toDify" ${dify ? 'checked' : ''}><b>Dify Knowledge</b></label>
    <div id="dbox" style="display:flex;flex-direction:column;gap:10px" ${dify ? '' : 'hidden'}>
      <label class="field">Knowledge<select id="dataset"><option>กำลังโหลด…</option></select></label>
      <fieldset><legend>การแบ่ง chunk</legend>
        <div class="seg" id="chunkMode"><button type="button" data-v="automatic" class="on">อัตโนมัติ</button><button type="button" data-v="custom">กำหนดเอง</button></div>
      </fieldset>
      <div class="row" id="chunkCustom" hidden>
        <label class="field" style="flex:1">ตัวแบ่ง<input type="text" id="sep" class="code" value="\\n## "></label>
        <label class="field" style="flex:1">สูงสุด (tokens)<input type="number" id="maxTokens" value="800"></label>
        <label class="field" style="flex:1">ซ้อนทับ<input type="number" id="overlap" value="80"></label>
      </div>
    </div>
  </aside>`;
}
function bindDest() {
  const sync = () => { $('#gbox').hidden = !$('#toGoogle').checked; $('#dbox').hidden = !$('#toDify').checked; };
  $('#toGoogle').onchange = sync;
  $('#toDify').onchange = () => { sync(); if ($('#toDify').checked) difyOptions($('#dataset')); };
  if ($('#toDify').checked) difyOptions($('#dataset'));
  if (ME.guest) {
    // Customers always get a new Doc in the company folder; they cannot pick another Doc or folder.
    $('input[name=target][value=existing]').closest('label').hidden = true;
    $('#existingRow').hidden = true; $('label[for=folder]').hidden = true; $('#folder').closest('.row').hidden = true;
    $('#folderNote').textContent = 'ไฟล์จะถูกบันทึกในโฟลเดอร์ Google Drive ของบริษัท';
  }
  $$('input[name=target]').forEach(r => r.onchange = () => { $('#existingRow').hidden = $('input[name=target]:checked').value !== 'existing'; });
  for (const id of ['layout', 'chunkMode']) {
    $('#' + id).addEventListener('click', (e) => {
      const b = e.target.closest('button'); if (!b) return;
      $$('button', $('#' + id)).forEach(x => { x.classList.toggle('on', x === b); x.setAttribute('aria-checked', x === b); });
      if (id === 'chunkMode') $('#chunkCustom').hidden = b.dataset.v !== 'custom';
    });
  }
  $$('[data-pick]').forEach(b => b.onclick = async () => {
    try {
      const d = await pick(b.dataset.pick); if (!d) return;
      if (b.dataset.pick === 'folder') { $('#folder').value = d.url || d.id; $('#folderNote').textContent = `โฟลเดอร์: ${d.name}`; }
      else $('#docId').value = d.url || d.id;
    } catch (e) { fail(e); }
  });
  const cf = $('#checkFolder');
  if (cf) cf.onclick = async () => {
    try { const f = await api('/api/drive/check-folder', { method: 'POST', body: { folder: $('#folder').value } }); $('#folderNote').textContent = `ใช้ได้: ${f.name}`; }
    catch (e) { $('#folderNote').textContent = e.message; }
  };
  api('/api/settings').then(s => { if (s.defaultFolderName) $('#folder').placeholder = `เว้นว่าง = ใช้ “${s.defaultFolderName}”`; }).catch(() => {});
}
function readDest() {
  return {
    toGoogle: $('#toGoogle').checked, toDify: $('#toDify').checked,
    target: $('input[name=target]:checked').value, docTitle: $('#docTitle').value, docId: $('#docId').value, folder: $('#folder').value.trim() || null,
    layout: $('#layout .on').dataset.v, format: $('input[name=format]:checked').value,
    difyDatasetId: $('#dataset').value || null,
    chunk: $('#chunkMode .on').dataset.v === 'custom' ? { mode: 'custom', separator: $('#sep').value, maxTokens: $('#maxTokens').value, overlap: $('#overlap').value } : null
  };
}

// ---------- Views ----------
const views = {};

views.login = () => {
  $('#topbar').hidden = true;
  app.innerHTML = `<section class="card login"><h1>Link2Doc</h1><p class="muted">${FEAT.public ? 'เข้าสู่ระบบผู้ดูแล' : 'แปลงหน้าเว็บและ PDF เป็น Markdown แล้วส่งเข้า Google Doc หรือ Dify'}</p>
    <form id="lf" style="display:flex;flex-direction:column;gap:10px;text-align:left">
      <label class="field">ชื่อผู้ใช้<input id="lu" type="text" autocomplete="username" required></label>
      <label class="field">รหัสผ่าน<input id="lp" type="password" autocomplete="current-password" required></label>
      <p class="small" id="lerr" style="color:#B42318" role="alert"></p>
      <button class="btn primary" type="submit">เข้าสู่ระบบ</button></form>
    ${FEAT.public ? '<a class="btn ghost" href="#/new">กลับไปหน้าแปลงไฟล์</a>' : '<a class="btn" href="/auth/google">เข้าสู่ระบบด้วย Google</a>'}</section>`;
  $('#lf').onsubmit = async (e) => {
    e.preventDefault(); $('#lerr').textContent = '';
    try { await api('/auth/login', { method: 'POST', body: { username: $('#lu').value, password: $('#lp').value } }); location.hash = '#/settings'; location.reload(); }
    catch (err) { $('#lerr').textContent = err.message; }
  };
};

views.new = () => {
  app.innerHTML = `${steps(1)}
    <div class="row between"><div><h1>วางลิงก์ที่ต้องการแปลงเป็น Markdown</h1><p class="muted">ระบบจะดึงเนื้อหาหลักของแต่ละหน้า แปลงเป็น Markdown แล้วเขียนลง Google Doc หรือ Dify ให้</p></div>${sourceSwitch('url')}</div>
    <div class="cols">
      <section class="card grow" aria-labelledby="links-h">
        <div class="row between"><h2 id="links-h">ลิงก์</h2><span class="small muted">บรรทัดละ 1 ลิงก์ หรือวางทั้งก้อน ระบบแยกให้</span></div>
        <label for="urls" class="sr">ลิงก์ที่จะดึง</label>
        <textarea id="urls" rows="10" class="code" placeholder="https://www.fda.moph.go.th/..."></textarea>
        <div class="row"><span class="chip ok" id="cnt">พบ 0 ลิงก์</span><span class="chip" id="dup">ซ้ำ 0</span><span class="chip" id="bad">รูปแบบไม่ถูกต้อง 0</span></div>
        <fieldset><legend>เนื้อหา</legend>
          <label class="check"><input type="checkbox" id="mainOnly" checked>เอาเฉพาะเนื้อหาหลัก (ตัดเมนู โฆษณา footer)</label>
          <label class="check"><input type="checkbox" id="keepLinks" checked>เก็บลิงก์</label>
          <label class="check"><input type="checkbox" id="header" checked>ใส่หัวเรื่อง: ชื่อหน้า, URL, วันที่ดึง</label>
          <label class="check"><input type="checkbox" id="keepImages">เก็บรูปภาพ (ลิงก์รูปต้นทาง)</label>
        </fieldset>
      </section>
      ${destBlock()}
    </div>
    ${scheduleBlock()}
    <div class="row end"><span class="muted small" style="margin-right:auto" id="summary"></span>
      <button class="btn" id="quick">ส่งทันที</button><button class="btn primary" id="go">ดึงข้อมูลและพรีวิว ${ICON.arrow}</button></div>`;
  bindDest(); bindSchedule();
  const parse = () => {
    const raw = $('#urls').value.split(/[\s,]+/).filter(Boolean);
    const seen = new Set(); let dup = 0, bad = 0; const ok = [];
    for (const r of raw) { try { const u = new URL(r); if (!/^https?:$/.test(u.protocol)) throw 0; const k = u.href; if (seen.has(k)) dup++; else { seen.add(k); ok.push(k); } } catch { bad++; } }
    $('#cnt').textContent = `พบ ${ok.length} ลิงก์`; $('#dup').textContent = `ซ้ำ ${dup}`; $('#bad').textContent = `รูปแบบไม่ถูกต้อง ${bad}`;
    $('#bad').className = 'chip' + (bad ? ' bad' : '');
    return ok;
  };
  $('#urls').oninput = parse;
  const submit = async (autoPublish) => {
    const urls = parse();
    if (!urls.length) return toast('วางลิงก์อย่างน้อย 1 ลิงก์');
    const d = readDest();
    if (!d.toGoogle && !d.toDify) return toast('เลือกปลายทางอย่างน้อย 1 ที่');
    try {
      const r = await api('/api/jobs', { method: 'POST', body: {
        source: 'url', urls, ...d, autoPublish, schedule: readSchedule(),
        mainOnly: $('#mainOnly').checked, keepLinks: $('#keepLinks').checked, header: $('#header').checked, keepImages: $('#keepImages').checked
      } });
      location.hash = `#/job/${r.id}`;
    } catch (e) { fail(e); }
  };
  $('#go').onclick = () => submit(false);
  $('#quick').onclick = () => submit(true);
};

views.pdf = () => {
  if (!FEAT.pdf) { app.innerHTML = `${sourceSwitch('pdf')}<div class="alert warn">ยังไม่ได้เปิดบริการแปลง PDF (ตั้งค่า PDF_WORKER_URL ใน Railway)</div>`; return; }
  const files = [];
  app.innerHTML = `<div class="row between"><div><h1>แปลง PDF เป็น Markdown</h1><p class="muted">อ่านได้ทั้ง PDF ที่มีข้อความและภาพสแกน (OCR ไทย-อังกฤษ) แล้วส่งเข้า Dify Knowledge หรือ Google Doc</p></div>${sourceSwitch('pdf')}</div>
    <div class="cols">
      <section class="grow" style="display:flex;flex-direction:column;gap:16px;min-width:0">
        <div class="card"><h2>ไฟล์</h2>
          <label class="drop" id="drop" for="pdfin">${ICON.upload}<b>ลากไฟล์ PDF มาวาง หรือคลิกเพื่อเลือก</b><span class="small muted">หลายไฟล์ได้ · ไฟล์ละไม่เกิน 500 MB</span></label>
          <input id="pdfin" type="file" accept="application/pdf" multiple class="sr">
          <div class="row"><label for="pdfurl" class="small muted">หรือลิงก์ PDF</label><input id="pdfurl" type="url" class="code" placeholder="https://…/ประกาศ.pdf" style="flex:1 1 260px;min-width:0"><button type="button" class="btn" id="addUrl">เพิ่ม</button></div>
          <div class="table" id="flist" hidden><table><thead><tr><th>ไฟล์</th><th style="width:160px">หน้า</th><th style="width:130px">สถานะ</th><th style="width:60px"></th></tr></thead><tbody></tbody></table></div>
        </div>
      </section>
      <div class="side" style="display:flex;flex-direction:column;gap:16px">
        <div class="card"><h2>การอ่าน PDF</h2>
          <label class="check"><input type="checkbox" id="ocr" checked>ตรวจหน้าสแกนอัตโนมัติ แล้ว OCR เฉพาะหน้านั้น</label>
          <label class="field">ภาษา OCR<select id="ocrLang"><option value="tha+eng">ไทย + อังกฤษ</option><option value="tha">ไทย</option><option value="eng">อังกฤษ</option></select></label>
          <label class="check"><input type="checkbox" id="stripHeaders" checked>ตัดหัวกระดาษ ท้ายกระดาษ เลขหน้า</label>
          <label class="check"><input type="checkbox" id="pageMarkers" checked>ใส่เลขหน้าต้นฉบับกำกับ [หน้า N]</label>
        </div>
        ${destBlock({ dify: true })}
      </div>
    </div>
    <div class="row end"><span class="muted small" style="margin-right:auto" id="psum">ยังไม่มีไฟล์</span>
      <button class="btn" id="review">ตรวจ Markdown ก่อน</button><button class="btn primary" id="send">แปลงและส่ง</button></div>`;
  bindDest();
  const tbody = $('#flist tbody');
  const render = () => {
    $('#flist').hidden = !files.length;
    tbody.innerHTML = files.map((f, i) => `<tr><td><div class="ellipsis"><b>${esc(f.name)}</b></div><div class="small muted">${f.pages ? `${num(f.pages)} หน้า` : ''}${f.size ? ` · ${(f.size / 1048576).toFixed(1)} MB` : ''}</div>
      ${f.pct != null && f.pct < 100 ? `<div class="progress" style="height:6px;margin-top:6px"><span style="width:${f.pct}%;background:#E9A23B"></span></div>` : ''}</td>
      <td><label class="sr" for="pg${i}">ช่วงหน้า</label><input id="pg${i}" data-i="${i}" type="text" class="code" value="${esc(f.pages_range || 'ทั้งหมด')}" style="min-height:36px"></td>
      <td>${f.error ? `<span class="chip bad" title="${esc(f.error)}">ผิดพลาด</span>` : f.fileId || f.url ? '<span class="chip ok">พร้อม</span>' : `<span class="chip run">อัปโหลด ${f.pct || 0}%</span>`}</td>
      <td><button class="btn ghost sm" data-del="${i}" aria-label="เอาออก">✕</button></td></tr>`).join('');
    $('#psum').textContent = files.length ? `${files.length} ไฟล์` : 'ยังไม่มีไฟล์';
  };
  tbody.addEventListener('input', (e) => { if (e.target.dataset.i) files[e.target.dataset.i].pages_range = e.target.value; });
  tbody.addEventListener('click', (e) => { const b = e.target.closest('[data-del]'); if (b) { files.splice(Number(b.dataset.del), 1); render(); } });
  const upload = (file) => {
    const f = { name: file.name, size: file.size, pct: 0 }; files.push(f); render();
    const fd = new FormData(); fd.append('file', file, file.name);
    const xhr = new XMLHttpRequest(); xhr.open('POST', '/api/pdf/upload');
    xhr.upload.onprogress = (e) => { if (e.lengthComputable) { f.pct = Math.round(e.loaded / e.total * 100); render(); } };
    xhr.onload = () => {
      try { const r = JSON.parse(xhr.responseText); if (xhr.status >= 300) throw new Error(r.detail || r.error); f.fileId = r.file_id; f.pages = r.pages; f.pct = 100; }
      catch (e) { f.error = e.message || 'อัปโหลดไม่สำเร็จ'; }
      render();
    };
    xhr.onerror = () => { f.error = 'อัปโหลดไม่สำเร็จ'; render(); };
    xhr.send(fd);
  };
  $('#pdfin').onchange = (e) => { [...e.target.files].forEach(upload); e.target.value = ''; };
  const drop = $('#drop');
  drop.ondragover = (e) => { e.preventDefault(); drop.classList.add('over'); };
  drop.ondragleave = () => drop.classList.remove('over');
  drop.ondrop = (e) => { e.preventDefault(); drop.classList.remove('over'); [...e.dataTransfer.files].filter(f => /pdf$/i.test(f.type) || /\.pdf$/i.test(f.name)).forEach(upload); };
  $('#addUrl').onclick = () => { const u = $('#pdfurl').value.trim(); if (!u) return; files.push({ name: u.split('/').pop() || u, url: u }); $('#pdfurl').value = ''; render(); };
  const submit = async (autoPublish) => {
    const ready = files.filter(f => (f.fileId || f.url) && !f.error);
    if (!ready.length) return toast('เพิ่มไฟล์ PDF ก่อน');
    if (files.some(f => !f.fileId && !f.url && !f.error)) return toast('รอให้อัปโหลดเสร็จก่อน');
    const d = readDest();
    if (!d.toGoogle && !d.toDify) return toast('เลือกปลายทางอย่างน้อย 1 ที่');
    try {
      const r = await api('/api/jobs', { method: 'POST', body: {
        source: 'pdf', files: ready.map(f => ({ fileId: f.fileId, url: f.url, name: f.name, pages: f.pages_range })), ...d, autoPublish,
        ocr: $('#ocr').checked, ocrLang: $('#ocrLang').value, stripHeaders: $('#stripHeaders').checked, pageMarkers: $('#pageMarkers').checked
      } });
      location.hash = `#/job/${r.id}`;
    } catch (e) { fail(e); }
  };
  $('#send').onclick = () => submit(true);
  $('#review').onclick = () => submit(false);
};

views.job = async (id) => {
  let lastStatus = null; let reviewState = null;
  const tick = async () => {
    let job;
    try { job = await api(`/api/jobs/${id}`); } catch (e) { app.innerHTML = `<div class="alert err">${esc(e.message)}</div>`; return; }
    if (job.status === 'review') { if (lastStatus !== 'review') { lastStatus = 'review'; reviewState = await renderReview(id); } return schedule(); }
    lastStatus = job.status;
    if (['done', 'partial', 'failed'].includes(job.status) && job.items.every(i => !['queued', 'fetching'].includes(i.status))) { renderDone(job); return; }
    renderProgress(job);
    schedule();
  };
  const schedule = () => { clearTimeout(pollTimer); if (lastStatus !== 'review') pollTimer = setTimeout(tick, 1500); };
  tick();
};

function renderProgress(job) {
  const n = job.items.length;
  const c = (s) => job.items.filter(i => s.includes(i.status)).length;
  const ok = c(['ready', 'written']); const run = c(['queued', 'fetching']); const bad = c(['failed']);
  const publishing = job.status === 'publishing';
  app.innerHTML = `${steps(publishing ? 4 : 2)}
    <section class="card">
      <div class="row between"><div><p class="small muted mono">งาน ${esc(job.id.slice(0, 8))} · ${fmtDate(job.created_at)}</p>
        <h1>${publishing ? 'กำลังส่งเข้า ' + (job.options.toDify && job.options.toGoogle === false ? 'Dify' : 'Google Doc') : `กำลังดึงข้อมูล ${n} รายการ`}</h1></div>
        <div class="row"><span><b style="font-size:22px">${ok}</b> <span class="muted">พร้อม</span></span><span><b style="font-size:22px;color:var(--warn)">${run}</b> <span class="muted">กำลังดึง</span></span><span><b style="font-size:22px;color:var(--err)">${bad}</b> <span class="muted">ล้มเหลว</span></span></div></div>
      <div class="progress" role="progressbar" aria-label="ความคืบหน้า" aria-valuenow="${Math.round((ok + bad) / n * 100)}" aria-valuemin="0" aria-valuemax="100">
        <span style="width:${ok / n * 100}%;background:var(--accent)"></span><span style="width:${bad / n * 100}%;background:#E07A73"></span></div>
    </section>
    <div class="table"><table><thead><tr><th style="width:44px">#</th><th>หน้าเว็บ / ไฟล์</th><th style="width:130px">สถานะ</th><th style="width:110px">ขนาด</th><th style="width:200px"></th></tr></thead><tbody>
    ${job.items.map((i, k) => `<tr><td class="muted">${k + 1}</td>
      <td><div class="ellipsis">${esc(i.title || (i.status === 'fetching' ? (i.source_type === 'pdf' ? `กำลังอ่าน PDF ${i.progress || 0}%` : 'กำลังดึง…') : i.file_name || '—'))}</div><div class="url ellipsis">${esc(i.url || i.file_name || '')}</div>
      ${i.error ? `<div class="alert err" style="margin-top:6px;padding:8px 12px">${esc(i.error)}</div>` : ''}</td>
      <td>${chip(STATUS, i.status)}</td><td class="small">${i.word_count ? num(i.word_count) + ' คำ' : '—'}</td>
      <td style="text-align:right">${i.status === 'failed' ? `<button class="btn sm" data-retry="${i.id}">ลองใหม่</button> <button class="btn ghost sm" data-skip="${i.id}">ข้าม</button>` : ''}</td></tr>`).join('')}
    </tbody></table></div>
    <div class="row end"><button class="btn" id="cancel" ${publishing ? 'hidden' : ''}>ยกเลิกงาน</button></div>`;
  app.onclick = async (e) => {
    const r = e.target.closest('[data-retry]'); const s = e.target.closest('[data-skip]');
    try {
      if (r) await api(`/api/items/${r.dataset.retry}/retry`, { method: 'POST' });
      if (s) await api(`/api/items/${s.dataset.skip}`, { method: 'PATCH', body: { skip: true } });
      if (e.target.id === 'cancel') { await api(`/api/jobs/${job.id}/cancel`, { method: 'POST' }); location.hash = '#/history'; }
    } catch (err) { fail(err); }
  };
}

async function renderReview(id) {
  const job = await api(`/api/jobs/${id}?markdown=1`);
  const items = job.items;
  let sel = items.findIndex(i => i.status === 'ready'); if (sel < 0) sel = 0;
  const dirty = new Set();
  const usable = () => items.filter(i => i.status === 'ready');
  app.onclick = null;
  app.innerHTML = `${steps(3)}
    <div class="row between"><h1>ตรวจ Markdown ก่อนส่ง</h1>
      <div class="row"><button class="btn" id="copyAll">คัดลอก MD ทั้งหมด</button><button class="btn" id="dlAll">ดาวน์โหลด .md</button><button class="btn primary" id="publish"></button></div></div>
    <div class="cols">
      <nav class="card side" aria-label="รายการ" style="flex:1 1 240px;padding:10px"><p class="small muted" style="margin:6px 10px 0">${job.layout === 'tab' ? 'แท็บใน Doc' : 'รายการ'}</p><div class="tabs-list" id="tl"></div></nav>
      <section class="grow" style="display:flex;flex-direction:column;gap:14px;min-width:0" id="ed"></section>
    </div>
    ${scheduleBlock('rs')}`;
  bindSchedule('rs');
  if (job.options.schedule) { const b = $(`#rs-freq [data-v=${job.options.schedule.frequency}]`); b?.click(); }
  const list = () => {
    $('#tl').innerHTML = items.map((it, i) => {
      const dot = it.status === 'failed' ? 'bad' : it.status === 'skipped' ? 'off' : (it.warnings || []).length ? 'warn' : '';
      return `<button type="button" data-i="${i}" class="${i === sel ? 'on' : ''}" aria-current="${i === sel}"><span class="small muted mono" style="width:18px">${i + 1}</span><span class="ellipsis" style="flex:1;max-width:none">${esc(it.tab_title || it.title || it.url || it.file_name)}</span><span class="dot ${dot}"></span></button>`;
    }).join('');
    $('#publish').textContent = `ส่ง ${usable().length} รายการ`;
  };
  const editor = () => {
    const it = items[sel];
    const failed = it.status === 'failed';
    $('#ed').innerHTML = `<div class="card" style="padding:16px 20px"><div class="row">
        <label for="tabname" class="small muted">ชื่อแท็บ</label><input id="tabname" type="text" value="${esc(it.tab_title || it.title || '')}" style="flex:1 1 240px;min-width:0">
        ${it.url ? `<a class="url ellipsis" style="max-width:280px" href="${esc(it.url)}" target="_blank" rel="noopener">${esc(it.url)}</a>` : ''}
        <label for="lfreq" class="small muted">ดึงซ้ำ</label><select id="lfreq" style="width:auto"><option value="">ตามงาน</option><option value="hourly">ทุกชั่วโมง</option><option value="daily">ทุกวัน</option><option value="weekly">ทุกสัปดาห์</option><option value="monthly">ทุกเดือน</option><option value="once">ไม่ดึงซ้ำ</option></select>
        <button class="btn" id="refetch">ดึงใหม่</button><button class="btn ghost danger" id="skip">${it.status === 'skipped' ? 'เอากลับมา' : 'ไม่เอารายการนี้'}</button></div></div>
      ${failed ? `<div class="alert err">${esc(it.error || 'ดึงไม่สำเร็จ')} · วางเนื้อหาเองในช่อง Markdown ได้ แล้วระบบจะใช้ข้อความนั้นแทน</div>` : ''}
      ${(it.warnings || []).length ? `<div class="alert warn" role="status"><b>ควรดู ${it.warnings.length} จุด:</b> ${it.warnings.map(esc).join(' · ')}</div>` : ''}
      <div class="split">
        <div class="card" style="padding:0;overflow:hidden"><div class="row between small muted" style="padding:10px 16px;border-bottom:1px solid #EDF1EF"><label for="md">Markdown</label><span>${it.word_count ? num(it.word_count) + ' คำ' : ''}</span></div>
          <textarea id="md" rows="24" class="code" style="border:0;border-radius:0;flex:1">${esc(it.markdown || '')}</textarea></div>
        <article class="card" style="padding:0;overflow:hidden" aria-label="พรีวิว"><div class="small muted" style="padding:10px 16px;border-bottom:1px solid #EDF1EF">พรีวิว</div><div class="preview" id="pv"></div></article>
      </div>`;
    $('#lfreq').value = it.schedule_override?.frequency || '';
    const pv = () => { $('#pv').innerHTML = marked.parse((it.markdown || '').replace(/^---\n[\s\S]*?\n---\n?/, '')); };
    pv();
    $('#md').oninput = (e) => { it.markdown = e.target.value; dirty.add(it.id); clearTimeout(editor.t); editor.t = setTimeout(pv, 250); };
    $('#tabname').oninput = (e) => { it.tab_title = e.target.value; dirty.add(it.id); list(); };
    $('#lfreq').onchange = (e) => { const v = e.target.value; it.schedule_override = v ? { ...(readSchedule('rs') || { time: '07:00', updateMode: 'replace' }), frequency: v } : null; dirty.add(it.id); };
    $('#skip').onclick = async () => { await api(`/api/items/${it.id}`, { method: 'PATCH', body: { skip: it.status !== 'skipped' } }); it.status = it.status === 'skipped' ? (it.markdown ? 'ready' : 'failed') : 'skipped'; list(); editor(); };
    $('#refetch').onclick = async () => { await api(`/api/items/${it.id}/retry`, { method: 'POST' }); location.reload(); };
  };
  $('#tl').onclick = (e) => { const b = e.target.closest('[data-i]'); if (!b) return; sel = Number(b.dataset.i); list(); editor(); };
  const save = async () => {
    for (const it of items.filter(i => dirty.has(i.id))) {
      await api(`/api/items/${it.id}`, { method: 'PATCH', body: { tabTitle: it.tab_title, markdown: it.markdown, schedule: it.schedule_override || undefined } });
      if (it.status === 'failed' && it.markdown?.trim()) it.status = 'ready';
    }
    dirty.clear();
  };
  const allMd = () => usable().map(i => i.markdown).join('\n\n---\n\n');
  $('#copyAll').onclick = async () => { try { await navigator.clipboard.writeText(allMd()); toast('คัดลอกแล้ว'); } catch { toast('คัดลอกไม่ได้ ให้ใช้ดาวน์โหลดแทน'); } };
  $('#dlAll').onclick = () => { if (usable().length === 1) download(usable()[0].tab_title || 'link2doc', usable()[0].markdown); else download(job.doc_title || 'link2doc', allMd()); };
  $('#publish').onclick = async () => {
    try {
      $('#publish').disabled = true;
      await save();
      if (!usable().length) { $('#publish').disabled = false; return toast('ไม่มีรายการที่พร้อมส่ง'); }
      await api(`/api/jobs/${id}/publish`, { method: 'POST', body: { schedule: readSchedule('rs') } });
      location.hash = `#/job/${id}`; views.job(id);
    } catch (e) { $('#publish').disabled = false; fail(e); }
  };
  list(); editor();
  return { save };
}

function renderDone(job) {
  const written = job.items.filter(i => i.status === 'written');
  const failed = job.items.filter(i => i.status === 'failed' || (i.error && i.status !== 'written'));
  const ok = job.status !== 'failed';
  const where = [job.options.toGoogle !== false && 'Google Doc', job.options.toDify && 'Dify'].filter(Boolean).join(' และ ');
  app.innerHTML = `<section class="card" style="max-width:880px;width:100%;margin:0 auto;padding:30px;gap:22px">
    <div class="row" style="align-items:flex-start"><span class="chip ${ok ? 'ok' : 'bad'}" style="width:52px;height:52px;justify-content:center;padding:0">${ok ? ICON.check : '!'}</span>
      <div style="flex:1;min-width:240px"><h1>${ok ? `ส่ง ${written.length} รายการเข้า ${where} แล้ว` : 'ส่งไม่สำเร็จ'}</h1>
      <p class="muted">${esc(job.doc_title || '')}${failed.length ? ` · ไม่ได้ส่ง ${failed.length} รายการ` : ''}</p></div></div>
    ${job.error ? `<div class="alert ${ok ? 'warn' : 'err'}" style="white-space:pre-line">${esc(job.error)}</div>` : ''}
    ${written.length ? `<div class="table"><table style="min-width:0"><thead><tr><th>${job.layout === 'tab' ? 'แท็บใน Doc' : 'รายการ'}</th><th style="width:110px">ขนาด</th><th style="width:120px"></th></tr></thead><tbody>
      ${written.map(i => `<tr><td><div class="ellipsis">${esc(i.tab_title || i.title)}</div></td><td class="small">${num(i.word_count)} คำ</td><td>${i.docUrl ? `<a href="${esc(i.docUrl)}" target="_blank" rel="noopener">เปิด ${ICON.ext}</a>` : i.dify_document_id ? '<span class="chip ok">Dify</span>' : ''}</td></tr>`).join('')}
    </tbody></table></div>` : ''}
    ${failed.length ? `<div class="alert err">${failed.map(i => `<div><span class="mono small">${esc(i.url || i.file_name)}</span> — ${esc(i.error || 'ล้มเหลว')}</div>`).join('')}</div>` : ''}
    <div class="row">${job.docUrl ? `<a class="btn primary" href="${esc(job.docUrl)}" target="_blank" rel="noopener">เปิด Google Doc ${ICON.ext}</a>` : ''}
      ${job.docUrl ? `<button class="btn" id="copyLink">คัดลอกลิงก์</button>` : ''}
      <a class="btn" href="#/schedules">จัดการรอบ</a><a class="btn ghost" href="#/new" style="margin-left:auto">เริ่มงานใหม่</a></div></section>`;
  const c = $('#copyLink'); if (c) c.onclick = async () => { try { await navigator.clipboard.writeText(job.docUrl); toast('คัดลอกลิงก์แล้ว'); } catch { toast(job.docUrl, 8000); } };
}

views.history = async () => {
  app.innerHTML = '<p class="muted">กำลังโหลด…</p>';
  let rows; try { rows = await api('/api/jobs'); } catch (e) { return fail(e); }
  let filter = 'all'; let qtext = '';
  app.innerHTML = `<div class="row between"><h1>ประวัติงาน</h1><a class="btn primary" href="#/new">+ งานใหม่</a></div>
    <div class="row"><label for="q" class="sr">ค้นหา</label><input id="q" type="search" placeholder="ค้นหาจากชื่อ Doc หรือโดเมน" style="flex:1 1 320px;min-width:0">
      <div class="row" role="group" aria-label="กรองสถานะ" id="flt">${[['all', 'ทั้งหมด'], ['done', 'สำเร็จ'], ['partial', 'บางส่วน'], ['failed', 'ล้มเหลว']].map(([v, t]) => `<button type="button" class="btn sm ${v === 'all' ? 'dark' : ''}" data-v="${v}" aria-pressed="${v === 'all'}">${t}</button>`).join('')}</div></div>
    <div class="table"><table><thead><tr><th>Google Doc / งาน</th><th style="width:150px">สร้างเมื่อ</th><th style="width:80px">ส่งแล้ว</th><th style="width:110px">รอบอัตโนมัติ</th><th style="width:110px">สถานะ</th><th style="width:220px"></th></tr></thead><tbody id="hb"></tbody></table></div>
    <p class="small muted">“ดึงใหม่” ดึงเนื้อหาล่าสุดของทุกรายการทันทีโดยไม่ต้องรอรอบ</p>`;
  const render = () => {
    const list = rows.filter(r => (filter === 'all' || r.status === filter) && (!qtext || `${r.doc_title} ${r.domains}`.toLowerCase().includes(qtext)));
    $('#hb').innerHTML = list.map(r => `<tr><td><a href="#/job/${r.id}" style="color:var(--ink);text-decoration:none"><div class="ellipsis"><b>${esc(r.doc_title)}</b></div></a><div class="small muted ellipsis">${{ tab: 'แท็บละลิงก์', h1: 'หัวข้อละลิงก์', doc: 'Doc ละลิงก์' }[r.layout]}${r.to_dify ? ' · Dify' : ''} · ${esc(r.domains || (r.source === 'pdf' ? 'PDF' : ''))}</div></td>
      <td class="small">${fmtDate(r.created_at)}</td><td class="small">${r.written}/${r.total}</td>
      <td>${Number(r.scheduled) ? `<span class="chip ok">${r.scheduled} รายการ</span>` : '<span class="small muted">—</span>'}</td><td>${chip(JOBSTATUS, r.status)}</td>
      <td style="text-align:right">${r.docUrl ? `<a class="btn sm" href="${esc(r.docUrl)}" target="_blank" rel="noopener">เปิด Doc</a>` : ''} ${Number(r.written) ? `<button class="btn sm" data-refresh="${r.id}">ดึงใหม่</button>` : ''}</td></tr>`).join('') || '<tr><td colspan="6" class="muted">ยังไม่มีงาน</td></tr>';
  };
  $('#q').oninput = (e) => { qtext = e.target.value.toLowerCase(); render(); };
  $('#flt').onclick = (e) => { const b = e.target.closest('button'); if (!b) return; filter = b.dataset.v; $$('#flt button').forEach(x => { x.classList.toggle('dark', x === b); x.setAttribute('aria-pressed', x === b); }); render(); };
  $('#hb').onclick = async (e) => { const b = e.target.closest('[data-refresh]'); if (!b) return; try { const r = await api(`/api/jobs/${b.dataset.refresh}/refresh`, { method: 'POST' }); toast(`กำลังดึงใหม่ ${r.queued} รายการ`); } catch (err) { fail(err); } };
  render();
};

views.schedules = async () => {
  let data; try { data = await api('/api/schedules'); } catch (e) { return fail(e); }
  const s = data.schedules;
  const active = s.filter(x => x.active).length;
  const needs = s.filter(x => !x.active && x.paused_reason && /ไม่สำเร็จ/.test(x.paused_reason)).length;
  const freqText = (x) => `${FREQ[x.frequency] || x.frequency}`;
  app.innerHTML = `<div class="row between"><div><h1>รอบอัตโนมัติ</h1><p class="muted">รายการที่ระบบดึงซ้ำตามรอบ และอัปเดต Doc / Dify ให้เมื่อเนื้อหาเปลี่ยน</p></div><button class="btn" id="pauseAll">หยุดทุกรอบชั่วคราว</button></div>
    <div class="grid4">
      <div class="stat"><span class="small muted">รอบที่เปิดอยู่</span><b>${active}</b><span class="small muted">จาก ${s.length} รายการ</span></div>
      <div class="stat"><span class="small muted">ดึงไปแล้ววันนี้</span><b>${num(data.stats?.today)}</b><span class="small muted">ครั้ง</span></div>
      <div class="stat"><span class="small muted">เนื้อหาเปลี่ยน (7 วัน)</span><b style="color:var(--accent-ink)">${num(data.stats?.changed7)}</b><span class="small muted">ครั้ง</span></div>
      <div class="stat"><span class="small muted">ต้องดูแล</span><b style="color:var(--err)">${needs}</b><span class="small muted">ระบบหยุดให้</span></div>
    </div>
    <div class="cols">
      <div class="table grow"><table><thead><tr><th>รายการ · ปลายทาง</th><th style="width:170px">รอบ</th><th style="width:120px">รอบล่าสุด</th><th style="width:80px">เปิดใช้</th><th style="width:170px"></th></tr></thead><tbody>
      ${s.map(x => `<tr><td><div class="ellipsis"><b>${esc(x.tab_title || x.title)}</b></div><div class="url ellipsis">${esc(x.url || x.file_name || '')}${x.doc_title ? ' → ' + esc(x.doc_title) : ''}</div></td>
        <td class="small">${freqText(x)}<div class="muted">${x.active ? (x.next_run ? 'ถัดไป ' + fmtDate(x.next_run) : '') : esc(x.paused_reason || 'หยุดอยู่')}</div></td>
        <td>${x.last_result ? chip({ unchanged: ['ไม่เปลี่ยน', ''], updated: ['อัปเดตแล้ว', 'ok'], failed: ['ล้มเหลว', 'bad'] }, x.last_result) : '<span class="small muted">ยังไม่รัน</span>'}</td>
        <td><button type="button" class="switch" role="switch" aria-checked="${x.active}" aria-label="เปิดหรือหยุดรอบ" data-toggle="${x.item_id}"><span></span></button></td>
        <td style="text-align:right"><button class="btn sm" data-run="${x.item_id}">ดึงตอนนี้</button> <button class="btn ghost sm" data-del="${x.item_id}" aria-label="ลบรอบ">ลบ</button></td></tr>`).join('') || '<tr><td colspan="5" class="muted">ยังไม่มีรอบอัตโนมัติ ตั้งได้ตอนสร้างงานใหม่</td></tr>'}
      </tbody></table></div>
      <aside class="card side"><h2>การเปลี่ยนแปลงล่าสุด</h2>
        ${data.changes.map(c => `<article style="display:flex;flex-direction:column;gap:6px;padding-bottom:12px;border-bottom:1px solid #EDF1EF">
          <div class="row between small muted"><span>${fmtDate(c.ran_at)}</span><span><span style="color:var(--accent-ink)">+${c.added}</span> / <span style="color:var(--err)">−${c.removed}</span> บรรทัด</span></div>
          <b>${esc(c.tab_title || c.title)}</b>
          <div class="diff">${c.before_line ? `<div class="del">− ${esc(c.before_line)}</div>` : ''}${c.after_line ? `<div class="add">+ ${esc(c.after_line)}</div>` : ''}</div>
          ${c.docUrl ? `<a href="${esc(c.docUrl)}" target="_blank" rel="noopener">เปิดแท็บ</a>` : ''}</article>`).join('') || '<p class="muted small">ยังไม่มีการเปลี่ยนแปลง</p>'}
      </aside>
    </div>`;
  app.onclick = async (e) => {
    const t = e.target.closest('[data-toggle]'); const r = e.target.closest('[data-run]'); const d = e.target.closest('[data-del]');
    try {
      if (t) { await api(`/api/schedules/${t.dataset.toggle}`, { method: 'PATCH', body: { active: t.getAttribute('aria-checked') !== 'true' } }); views.schedules(); }
      if (r) { await api(`/api/schedules/${r.dataset.run}/run`, { method: 'POST' }); toast('กำลังดึงใหม่'); }
      if (d) { if (d.dataset.confirm) { await api(`/api/schedules/${d.dataset.del}`, { method: 'DELETE' }); views.schedules(); } else { d.dataset.confirm = '1'; d.textContent = 'ยืนยันลบ'; d.classList.add('danger'); } }
      if (e.target.id === 'pauseAll') { const x = await api('/api/schedules/pause-all', { method: 'POST' }); toast(`หยุด ${x.paused} รอบแล้ว`); views.schedules(); }
    } catch (err) { fail(err); }
  };
};

views.settings = async () => {
  let s; try { s = await api('/api/settings'); } catch (e) { return fail(e); }
  app.onclick = null;
  app.innerHTML = `<h1>ตั้งค่า</h1>
    <section class="card"><div class="row between"><div><h2>${ME.admin ? 'Google Drive บริษัท' : 'บัญชี Google'}</h2><p class="small muted">${ME.admin ? (ME.google ? `เชื่อมกับ ${esc(ME.google_email || '')} · ไฟล์ของลูกค้าทุกคนจะเขียนลง Drive นี้` : 'ยังไม่ได้เชื่อม ลูกค้าแปลงไฟล์ได้แต่ยังส่งเข้า Drive ไม่ได้') : esc(ME.email)} · สิทธิ์ Drive: ${FEAT.driveScope === 'full' ? 'ทั้ง Drive (วางลิงก์โฟลเดอร์ได้)' : 'เฉพาะไฟล์ที่แอปสร้างหรือที่คุณเลือก'}</p></div>
      <span class="chip ${ME.google ? 'ok' : 'bad'}">${ME.google ? 'เชื่อมแล้ว' : 'ยังไม่เชื่อม'}</span>
      ${ME.admin ? (FEAT.googleConfigured ? `<a class="btn primary" href="/auth/google">${ME.google ? 'เปลี่ยนบัญชี Google' : 'เชื่อม Google Drive'}</a>` : '<span class="small muted">ใส่ GOOGLE_CLIENT_ID / SECRET ใน Railway ก่อน</span>') : ''}
      ${ME.google ? '<button class="btn danger" id="disc">ยกเลิกการเชื่อม</button>' : ''}</div>
      <label class="field" for="dfolder">โฟลเดอร์เริ่มต้นใน Drive (รวม Shared drive)</label>
      <div class="row"><input id="dfolder" type="url" class="code" placeholder="${esc(s.defaultFolderName ? `ตอนนี้: ${s.defaultFolderName}` : 'ถ้าไม่ตั้ง ระบบสร้างโฟลเดอร์ “Link2Doc” ให้')}" style="flex:1 1 320px;min-width:0">${FEAT.picker ? '<button class="btn" id="pickF">เลือกจาก Drive</button>' : ''}</div></section>
    <section class="card"><div class="row between"><div><h2>Dify</h2><p class="small muted">ใช้ Knowledge API ส่ง Markdown เข้าเป็นเอกสาร และอัปเดตเอกสารเดิมเมื่อดึงซ้ำ</p></div><span class="chip ${s.dify ? 'ok' : ''}">${s.dify ? 'เชื่อมแล้ว' : 'ยังไม่เชื่อม'}</span></div>
      <div class="split"><label class="field">Dify API URL<input id="durl" type="url" class="code" value="${esc(s.dify?.base_url || '')}" placeholder="https://dify.company.co.th/v1"></label>
        <label class="field">Knowledge API key<input id="dkey" type="password" class="code" placeholder="${s.dify ? 'บันทึกไว้แล้ว ใส่ใหม่เพื่อเปลี่ยน' : 'dataset-…'}"></label>
        <label class="field">Knowledge เริ่มต้น<select id="dds"><option value="">—</option></select></label>
        <label class="field">วิธีทำ index<select id="didx"><option value="high_quality">High quality (embedding)</option><option value="economy" ${s.dify?.indexing_technique === 'economy' ? 'selected' : ''}>Economy (keyword)</option></select></label></div>
      <div class="row end"><button class="btn" id="dtest">ทดสอบการเชื่อมต่อ</button><button class="btn dark" id="dsave">บันทึก Dify</button></div></section>
    <section class="card"><div class="row between"><div><h2>กฎตัดเนื้อหารายเว็บ</h2><p class="small muted">ใช้ก่อนตัวตัดเนื้อหาอัตโนมัติ ระบุเป็น CSS selector</p></div></div>
      <div class="row"><input id="rd" type="text" placeholder="โดเมน เช่น narcotic.fda.moph.go.th" style="flex:1 1 220px"><input id="ri" type="text" class="code" placeholder="เอาเฉพาะ เช่น .entry-content" style="flex:1 1 180px"><input id="re" type="text" class="code" placeholder="ตัดออก เช่น .share, .related" style="flex:1 1 180px"></div>
      <div class="row end"><input id="rurl" type="url" class="code" placeholder="URL สำหรับทดสอบ" style="flex:1 1 260px"><button class="btn" id="rtest">ทดสอบกับ URL</button><button class="btn dark" id="radd">บันทึกกฎ</button></div>
      <pre id="rprev" class="mono small" style="white-space:pre-wrap;max-height:240px;overflow:auto;background:#F7F9F8;padding:12px;border-radius:10px" hidden></pre>
      <div class="table"><table style="min-width:620px"><thead><tr><th>โดเมน</th><th>เอาเฉพาะ</th><th>ตัดออก</th><th style="width:80px"></th></tr></thead><tbody>
      ${s.rules.map(r => `<tr><td>${esc(r.domain)}</td><td class="mono small">${esc(r.include_sel || '')}</td><td class="mono small">${esc(r.exclude_sel || '')}</td><td><button class="btn ghost sm" data-rdel="${r.id}">ลบ</button></td></tr>`).join('') || '<tr><td colspan="4" class="muted">ยังไม่มีกฎ</td></tr>'}
      </tbody></table></div></section>
    <section class="card"><h2>เทมเพลตหัวเรื่อง</h2><label class="field" for="tpl">ใส่ไว้บนสุดของทุกรายการ ใช้ตัวแปร {title} {url} {fetched_at}</label>
      <textarea id="tpl" rows="6" class="code">${esc(s.headerTemplate || '---\ntitle: {title}\nsource_url: {url}\nfetched_at: {fetched_at}\n---')}</textarea>
      <div class="row end"><button class="btn primary" id="save">บันทึกการตั้งค่า</button></div></section>`;
  if (s.dify) api('/api/dify/datasets').then(r => { $('#dds').innerHTML = '<option value="">—</option>' + r.data.map(d => `<option value="${esc(d.id)}" ${s.dify.default_dataset_id === d.id ? 'selected' : ''}>${esc(d.name)}</option>`).join(''); }).catch(() => {});
  if ($('#disc')) $('#disc').onclick = async () => { await api('/api/google/disconnect', { method: 'POST' }); if (!ME.admin) location.hash = '#/login'; location.reload(); };
  const pf = $('#pickF'); if (pf) pf.onclick = async () => { const d = await pick('folder').catch(fail); if (d) $('#dfolder').value = d.url || d.id; };
  $('#save').onclick = async () => { try { const r = await api('/api/settings', { method: 'PUT', body: { headerTemplate: $('#tpl').value, defaultFolder: $('#dfolder').value.trim() || null } }); toast(r.folderName ? `บันทึกแล้ว · โฟลเดอร์: ${r.folderName}` : 'บันทึกแล้ว'); } catch (e) { fail(e); } };
  const dsave = () => api('/api/dify', { method: 'PUT', body: { baseUrl: $('#durl').value.trim(), apiKey: $('#dkey').value.trim() || null, defaultDatasetId: $('#dds').value || null, indexing: $('#didx').value } });
  $('#dsave').onclick = async () => { try { await dsave(); toast('บันทึก Dify แล้ว'); views.settings(); } catch (e) { fail(e); } };
  $('#dtest').onclick = async () => { try { await dsave(); const r = await api('/api/dify/datasets'); toast(`เชื่อมได้ · พบ ${r.data.length} Knowledge`); views.settings(); } catch (e) { fail(e); } };
  $('#radd').onclick = async () => { try { await api('/api/site-rules', { method: 'POST', body: { domain: $('#rd').value, include: $('#ri').value, exclude: $('#re').value } }); views.settings(); } catch (e) { fail(e); } };
  $('#rtest').onclick = async () => { try { const r = await api('/api/site-rules/test', { method: 'POST', body: { url: $('#rurl').value, include: $('#ri').value, exclude: $('#re').value } }); $('#rprev').hidden = false; $('#rprev').textContent = `# ${r.title}\n\n${r.preview}`; } catch (e) { fail(e); } };
  app.onclick = async (e) => { const b = e.target.closest('[data-rdel]'); if (b) { await api(`/api/site-rules/${b.dataset.rdel}`, { method: 'DELETE' }); views.settings(); } };
};

// ---------- Router ----------
async function route() {
  clearTimeout(pollTimer); app.onclick = null;
  const [, name = 'new', arg] = (location.hash || '#/new').split('/');
  if (!ME && name !== 'login') { location.hash = '#/login'; return; }
  $('#topbar').hidden = !ME;
  $$('.mainnav a').forEach(a => a.classList.toggle('on', a.dataset.nav === (name === 'pdf' || name === 'job' ? 'new' : name)));
  if (ME?.guest && name === 'settings') { location.hash = '#/new'; return; }
  (views[name] || views.new)(arg);
  if (ME?.guest && !ME.ready && (name === 'new' || name === 'pdf')) {
    app.insertAdjacentHTML('afterbegin', '<div class="alert" role="status" style="margin-bottom:16px;background:#FFF4DE;color:#7A4B00">ตอนนี้แปลงและพรีวิว Markdown ได้แล้ว แต่ระบบยังไม่ได้เชื่อม Google Drive ของบริษัท จึงยังส่งเข้า Drive ไม่ได้</div>');
  }
  window.scrollTo(0, 0);
}

(async () => {
  const me = await api('/api/me').catch(() => ({ user: null }));
  ME = me.user; FEAT = me.features || {};
  if (ME) { $('#email').textContent = ME.guest ? 'ผู้ใช้ทั่วไป' : ME.email; $('#avatar').textContent = ME.guest ? '?' : (ME.name || ME.email)[0].toUpperCase(); }
  $('#logout').onclick = async () => { await api('/auth/logout', { method: 'POST' }); location.hash = FEAT.public ? '#/new' : '#/login'; location.reload(); };
  if (ME?.guest) {
    $('[data-nav=settings]').hidden = true;
    $('#logout').textContent = 'ผู้ดูแล';
    $('#logout').onclick = () => { location.hash = '#/login'; };
  }
  window.addEventListener('hashchange', route);
  if (ME && (!location.hash || location.hash === '#/login')) location.hash = '#/new'; else route();
})();
