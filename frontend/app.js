"use strict";

const $ = (sel) => document.querySelector(sel);
const DAY_NAMES = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const DAY_NUM = [1, 2, 3, 4, 5, 6, 0]; // Mon..Sun → Go time.Weekday
const COLOR_VAR = {
  Available: "--available", Busy: "--busy", DoNotDisturb: "--dnd",
  BeRightBack: "--brb", Away: "--away", Offline: "--offline",
};
let state = null;
let preview = null;
let editing = null; // schedule being edited

function cssVar(name) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim(); }
function colorFor(p) { return cssVar(COLOR_VAR[p] || "--none"); }
function labelFor(p) { return (state?.presences.find((x) => x.id === p) || {}).label || p; }

async function api(path, body) {
  const opts = body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function fmtTime(iso) {
  const d = new Date(iso);
  const now = new Date();
  const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  if (d.toDateString() === now.toDateString()) return hm;
  if (d - now < 7 * 864e5) return d.toLocaleDateString([], { weekday: "short" }) + " " + hm;
  return d.toLocaleDateString() + " " + hm;
}

function text(el, s) { el.textContent = s; return el; }
function h(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  Object.assign(el, props);
  for (const c of children) el.append(c);
  return el;
}

// ---- status -----------------------------------------------------------------

function renderStatus() {
  const st = state.status;
  const target = $("#st-target");
  target.className = st.ready ? "" : "warn";
  text(target, (st.ready ? "✓ " : "⚠ ") + (st.info || "…"));
  $("#btn-grant").hidden = !(state.platform === "darwin" && /permission/i.test(st.info || ""));

  const cur = st.current;
  $("#st-dot").style.background = cur ? colorFor(cur.presence) : cssVar("--none");
  let now = cur ? `${cur.label} — ${cur.schedule}, until ${fmtTime(cur.until)}` : "No schedule active — Teams sets your status automatically";
  if (st.paused) now += "  (paused)";
  text($("#st-current"), now);

  text($("#st-next"), st.hasNext
    ? `${fmtTime(st.nextAt)} → ${st.next ? `${st.next.label} (${st.next.schedule})` : "automatic"}`
    : "None in the next week");

  const last = $("#st-last");
  last.className = st.lastError ? "warn" : "";
  if (st.busy) text(last, "Changing Teams status…");
  else if (st.lastError) text(last, `⚠ ${st.lastError}` + (st.retryAt && !st.retryAt.startsWith("0001") ? ` — retrying at ${fmtTime(st.retryAt)}` : ""));
  else if (st.lastUpdate && !st.lastUpdate.startsWith("0001")) text(last, `Set to ${st.applied || "automatic"} at ${fmtTime(st.lastUpdate)}`);
  else text(last, "—");
  last.title = st.lastDetail || "";

  $("#st-awake").value = state.settings.keepAwake;
  const awake = $("#st-awake-state");
  text(awake, st.sleepPrevented ? "Active: the computer won't idle-sleep" : state.settings.keepAwake === "off" ? "" : "Standing by");
  awake.className = st.sleepPrevented ? "ok" : "muted";
  text($("#btn-pause"), st.paused ? "Resume" : "Pause");
}

// ---- schedules table --------------------------------------------------------

function describeBlock(b) {
  const days = DAY_NUM.filter((d) => b.days.includes(d)).map((d) => DAY_NAMES[DAY_NUM.indexOf(d)]);
  const dayText = days.length === 7 ? "Every day" : days.join(", ");
  return `${dayText} ${b.start}–${b.end} → ${labelFor(b.presence)}`;
}

function renderSchedules() {
  const tbody = $("#sched-table tbody");
  tbody.replaceChildren();
  const list = [...state.schedules].sort((a, b) => a.priority - b.priority);
  if (!list.length) {
    tbody.append(h("tr", {}, h("td", { colSpan: 6, className: "empty", textContent: "No schedules yet. Click “Add schedule”." })));
    return;
  }
  for (const s of list) {
    const toggle = h("input", { type: "checkbox", checked: s.enabled, title: "Enable/disable" });
    toggle.addEventListener("change", () => api("/api/schedule/enable", { id: s.id, enabled: toggle.checked }).then(refresh));
    const dates = s.validFrom || s.validUntil ? `${s.validFrom || "…"} → ${s.validUntil || "…"}` : "Always";
    const blocks = s.blocks.map(describeBlock);
    const edit = h("button", { className: "link", textContent: "Edit" });
    edit.addEventListener("click", () => openEditor(s));
    const dup = h("button", { className: "link", textContent: "Duplicate" });
    dup.addEventListener("click", () => openEditor({ ...structuredClone(s), id: "", name: `${s.name} (copy)`, enabled: false }));
    const del = h("button", { className: "link", textContent: "Delete" });
    del.addEventListener("click", async () => {
      if (confirm(`Delete “${s.name}”?`)) { await api("/api/schedule/delete", { id: s.id }); refresh(); }
    });
    const row = h("tr", {},
      h("td", {}, toggle),
      h("td", { textContent: s.priority }),
      h("td", { textContent: s.name }),
      h("td", { textContent: dates }),
      h("td", { className: "blocks-cell", textContent: blocks.join("; "), title: blocks.join("\n") }),
      h("td", { className: "row-actions" }, edit, dup, del));
    row.addEventListener("dblclick", () => openEditor(s));
    tbody.append(row);
  }
}

// ---- week preview -----------------------------------------------------------

const LABEL_W = 40, HEAD_H = 18;

function drawWeek() {
  if (!preview) return;
  const canvas = $("#week");
  const dpr = window.devicePixelRatio || 1;
  const width = canvas.clientWidth, height = 250;
  canvas.width = width * dpr;
  canvas.height = height * dpr;
  canvas.style.height = height + "px";
  const ctx = canvas.getContext("2d");
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, width, height);
  const gridW = width - LABEL_W - 4, rowH = (height - HEAD_H) / 7, slotW = gridW / 96;
  ctx.font = "11px -apple-system, system-ui, sans-serif";
  ctx.fillStyle = cssVar("--muted");
  ctx.textAlign = "center";
  for (let hr = 0; hr <= 24; hr += 3) ctx.fillText(String(hr).padStart(2, "0"), LABEL_W + hr * 4 * slotW, 12);
  for (let d = 0; d < 7; d++) {
    const y = HEAD_H + d * rowH;
    ctx.textAlign = "right";
    ctx.fillStyle = d === preview.today ? cssVar("--text") : cssVar("--muted");
    ctx.font = (d === preview.today ? "600 " : "") + "12px -apple-system, system-ui, sans-serif";
    ctx.fillText(DAY_NAMES[d], LABEL_W - 6, y + rowH / 2 + 4);
    preview.days[d].forEach((slot, i) => {
      ctx.fillStyle = slot.p ? colorFor(slot.p) : cssVar("--none");
      ctx.fillRect(LABEL_W + i * slotW, y + 2, slotW + 0.5, rowH - 4);
    });
  }
  ctx.strokeStyle = cssVar("--card");
  ctx.lineWidth = 1;
  for (let hr = 1; hr < 24; hr++) {
    const x = Math.round(LABEL_W + hr * 4 * slotW) + 0.5;
    ctx.beginPath(); ctx.moveTo(x, HEAD_H); ctx.lineTo(x, HEAD_H + 7 * rowH); ctx.stroke();
  }
  const nowX = LABEL_W + (preview.nowMinutes / 15) * slotW;
  const nowY = HEAD_H + preview.today * rowH;
  ctx.strokeStyle = cssVar("--text");
  ctx.lineWidth = 2;
  ctx.beginPath(); ctx.moveTo(nowX, nowY); ctx.lineTo(nowX, nowY + rowH); ctx.stroke();
}

function renderLegend() {
  const legend = $("#legend");
  legend.replaceChildren();
  for (const p of state.presences) {
    const i = h("i"); i.style.background = colorFor(p.id);
    legend.append(h("span", {}, i, p.label));
  }
  const i = h("i"); i.style.background = cssVar("--none");
  legend.append(h("span", {}, i, "Automatic (no schedule)"));
}

$("#week").addEventListener("mousemove", (e) => {
  const tip = $("#tip");
  if (!preview) return;
  const canvas = e.currentTarget;
  const rowH = (250 - HEAD_H) / 7, slotW = (canvas.clientWidth - LABEL_W - 4) / 96;
  const d = Math.floor((e.offsetY - HEAD_H) / rowH), i = Math.floor((e.offsetX - LABEL_W) / slotW);
  if (d < 0 || d > 6 || i < 0 || i > 95) { tip.hidden = true; return; }
  const slot = preview.days[d][i];
  const hhmm = `${String(Math.floor(i / 4)).padStart(2, "0")}:${String((i % 4) * 15).padStart(2, "0")}`;
  tip.textContent = `${DAY_NAMES[d]} ${hhmm}\n${slot.p ? `${labelFor(slot.p)} — ${slot.s}` : "Automatic (no schedule)"}`;
  tip.style.left = e.offsetX + "px";
  tip.style.top = e.offsetY + "px";
  tip.hidden = false;
});
$("#week").addEventListener("mouseleave", () => { $("#tip").hidden = true; });
window.addEventListener("resize", drawWeek);
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => { drawWeek(); renderLegend(); renderStatus(); });

// ---- schedule editor ----------------------------------------------------------

function blockRow(b) {
  const tr = h("tr");
  DAY_NUM.forEach((d, idx) => {
    tr.append(h("td", {}, h("input", { type: "checkbox", checked: b.days.includes(d), title: DAY_NAMES[idx], dataset: { day: d } })));
  });
  tr.append(h("td", {}, h("input", { type: "time", value: b.start, required: true, className: "start" })));
  tr.append(h("td", {}, h("input", { type: "time", value: b.end, required: true, className: "end" })));
  const sel = h("select", { className: "presence" });
  for (const p of state.presences) sel.append(h("option", { value: p.id, textContent: p.label, selected: p.id === b.presence }));
  tr.append(h("td", {}, sel));
  const rm = h("button", { type: "button", className: "link", textContent: "Remove" });
  rm.addEventListener("click", () => tr.remove());
  tr.append(h("td", {}, rm));
  return tr;
}

function openEditor(s) {
  editing = s ? structuredClone(s) : { id: "", name: "", priority: 10, enabled: true, blocks: [{ days: [1, 2, 3, 4, 5], start: "09:00", end: "17:00", presence: "Available" }] };
  const f = $("#form-schedule");
  text($("#sched-title"), s && s.id ? "Edit schedule" : "New schedule");
  f.name.value = editing.name;
  f.priority.value = editing.priority;
  f.enabled.checked = editing.enabled;
  f.validFrom.value = editing.validFrom || "";
  f.validUntil.value = editing.validUntil || "";
  $("#blocks").replaceChildren(...editing.blocks.map(blockRow));
  $("#sched-error").hidden = true;
  $("#dlg-schedule").showModal();
}

$("#btn-add").addEventListener("click", () => openEditor(null));
$("#btn-add-block").addEventListener("click", () => $("#blocks").append(blockRow({ days: [1, 2, 3, 4, 5], start: "09:00", end: "17:00", presence: "Busy" })));

$("#form-schedule").addEventListener("submit", async (e) => {
  if (e.submitter?.value !== "save") return;
  e.preventDefault();
  const f = e.target;
  const blocks = [...$("#blocks").children].map((tr) => ({
    days: [...tr.querySelectorAll("input[data-day]")].filter((c) => c.checked).map((c) => Number(c.dataset.day)),
    start: tr.querySelector(".start").value,
    end: tr.querySelector(".end").value,
    presence: tr.querySelector(".presence").value,
  }));
  const sched = {
    ...editing,
    name: f.name.value.trim(),
    priority: Number(f.priority.value),
    enabled: f.enabled.checked,
    validFrom: f.validFrom.value,
    validUntil: f.validUntil.value,
    blocks,
  };
  try {
    await api("/api/schedule", sched);
    $("#dlg-schedule").close();
    refresh();
  } catch (err) {
    const el = $("#sched-error");
    el.textContent = "Can't save: " + err.message;
    el.hidden = false;
  }
});

// ---- settings -----------------------------------------------------------------

$("#btn-settings").addEventListener("click", () => {
  const f = $("#form-settings");
  const s = state.settings;
  f.target.value = s.target;
  f.checkIntervalSeconds.value = s.checkIntervalSeconds;
  f.resetOnExit.checked = s.resetOnExit;
  f.startHidden.checked = s.startHidden;
  f.launchAtLogin.checked = state.launchAtLogin;
  f.keepAwake.value = s.keepAwake;
  f.keepDisplayAwake.checked = s.keepDisplayAwake;
  text($("#perm-note"), state.platform === "darwin"
    ? "macOS: allow “Teams Status Scheduler” under System Settings → Privacy & Security → Accessibility."
    : "");
  text($("#version"), `Version ${state.version}`);
  $("#settings-error").hidden = true;
  $("#dlg-settings").showModal();
});

$("#form-settings").addEventListener("submit", async (e) => {
  if (e.submitter?.value !== "save") return;
  e.preventDefault();
  const f = e.target;
  try {
    await api("/api/settings", {
      ...state.settings,
      target: f.target.value,
      checkIntervalSeconds: Number(f.checkIntervalSeconds.value),
      resetOnExit: f.resetOnExit.checked,
      startHidden: f.startHidden.checked,
      launchAtLogin: f.launchAtLogin.checked,
      keepAwake: f.keepAwake.value,
      keepDisplayAwake: f.keepDisplayAwake.checked,
    });
    $("#dlg-settings").close();
    refresh();
  } catch (err) {
    const el = $("#settings-error");
    el.textContent = err.message;
    el.hidden = false;
  }
});

$("#btn-inspect").addEventListener("click", async () => {
  if (!confirm("This opens your Teams profile and status menus to list the controls the app can see. Your status isn't changed. It can take up to a minute. Continue?")) return;
  const btn = $("#btn-inspect");
  btn.disabled = true;
  text(btn, "Inspecting…");
  try {
    const r = await api("/api/inspect", {});
    text($("#inspect-note"), `Saved to ${r.path}. If a status change fails, compare these names with the patterns in “Edit UI labels…”.`);
    text($("#inspect-text"), r.text);
    $("#dlg-inspect").showModal();
  } catch (err) {
    alert(err.message);
  } finally {
    btn.disabled = false;
    text(btn, "Inspect Teams UI…");
  }
});
$("#btn-labels").addEventListener("click", () => api("/api/action", { action: "openLabels" }).catch((e) => alert(e.message)));
$("#btn-folder").addEventListener("click", () => api("/api/action", { action: "openFolder" }).catch((e) => alert(e.message)));

// ---- status actions -------------------------------------------------------------

$("#btn-pause").addEventListener("click", () => api("/api/action", { action: state.status.paused ? "resume" : "pause" }).then(refresh));
$("#btn-apply").addEventListener("click", () => api("/api/action", { action: "apply" }).then(() => setTimeout(refresh, 300)));
$("#btn-grant").addEventListener("click", () => api("/api/action", { action: "grant" }).then(refresh));
$("#st-awake").addEventListener("change", (e) => api("/api/action", { action: "keepAwake", mode: e.target.value }).then(() => setTimeout(refresh, 200)));

// ---- refresh loop -----------------------------------------------------------------

let lastSchedules = "";
async function refresh() {
  try {
    state = await api("/api/state");
  } catch {
    return;
  }
  renderStatus();
  const sig = JSON.stringify(state.schedules);
  if (sig !== lastSchedules || !preview) {
    lastSchedules = sig;
    renderSchedules();
    renderLegend();
    preview = await api("/api/preview");
  }
  drawWeek();
}

refresh();
setInterval(refresh, 2000);
setInterval(async () => { preview = await api("/api/preview"); drawWeek(); }, 60000);
