// Progressive enhancements only: every page works without JavaScript.

// Copy buttons: data-copy="#id" copies that element's text or value,
// data-copy-text the attribute's own value, data-copy-list="#id" the
// values of the list's checkboxes (only the ticked ones with data-checked),
// and data-copy-url what that address gives (a whole list's export).
document.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-copy], [data-copy-text], [data-copy-list], [data-copy-url]");
  if (!btn) return;
  if (btn.dataset.copyUrl) {
    const got = fetch(btn.dataset.copyUrl, { headers: { Accept: "text/plain" } })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("HTTP " + r.status))))
      .then((t) => t.trim() + "\n");
    // A ClipboardItem given the promise keeps the click's permission while
    // the list loads (Safari's rule); elsewhere, write once it's here.
    const write = window.ClipboardItem
      ? navigator.clipboard.write([new ClipboardItem({ "text/plain": got.then((t) => new Blob([t], { type: "text/plain" })) })])
      : got.then((t) => navigator.clipboard.writeText(t));
    write.then(() => copied(btn), () => copied(btn, "Couldn't copy"));
    return;
  }
  let text;
  if (btn.dataset.copyText !== undefined) {
    text = btn.dataset.copyText;
  } else if (btn.dataset.copyList) {
    const sel = "input.pick" + ("checked" in btn.dataset ? ":checked" : "");
    text = [...document.querySelectorAll(btn.dataset.copyList + " " + sel)].filter(shown).map((i) => i.value).join("\n");
  } else {
    const el = document.querySelector(btn.dataset.copy);
    if (!el) return;
    text = "value" in el ? el.value : el.textContent;
  }
  if (!text.trim()) return;
  navigator.clipboard.writeText(text.trim() + "\n").then(() => copied(btn));
});

// copied says so on the button: in its label, or by a mark on an icon.
function copied(btn, say = "Copied") {
  const label = btn.querySelector(".label");
  if (!label) {
    btn.classList.add("copied");
    setTimeout(() => btn.classList.remove("copied"), 1200);
    return;
  }
  const was = label.textContent;
  label.textContent = say;
  setTimeout(() => { label.textContent = was; }, 1500);
}

// shown reports whether an element is laid out (rows only wide screens show aren't).
function shown(el) { return el.offsetParent !== null; }

// Ticking targets shows the selection bar; data-select-all ticks them all.
document.addEventListener("change", (e) => {
  const box = e.target;
  const list = box.closest("section, form");
  if (!list || !(box.matches("input.pick") || box.matches("[data-select-all]"))) return;
  const picks = [...list.querySelectorAll("input.pick")].filter(shown);
  if (box.matches("[data-select-all]")) for (const p of picks) p.checked = box.checked;
  const n = picks.filter((p) => p.checked).length;
  const bar = list.querySelector("[data-selbar]");
  if (bar) {
    bar.hidden = n === 0;
    bar.querySelector("[data-selcount]").textContent = n + " selected";
  }
  const all = list.querySelector("[data-select-all]");
  if (all) {
    all.checked = n > 0 && n === picks.length;
    all.indeterminate = n > 0 && n < picks.length;
  }
});

// Forms with data-confirm ask first.
document.addEventListener("submit", (e) => {
  const msg = e.target.dataset.confirm;
  if (msg && !window.confirm(msg)) e.preventDefault();
});

// Theme: the top bar's menu sets light or dark ("system" follows the
// system) and the palette; Settings' picker sets the palette too. Both are
// kept in this browser (theme.js applies them on later pages) and every
// control shows the current choice.
(() => {
  const root = document.documentElement;
  const menu = document.querySelector("[data-theme-menu]");
  const settings = document.querySelector("[data-palettes]");
  const dark = window.matchMedia("(prefers-color-scheme: dark)");
  const names = { system: "System", light: "Light", dark: "Dark" };
  const mode = () => root.dataset.theme || "system";
  const palette = () => root.dataset.palette || "midnight";
  const save = (key, v) => { try { localStorage.setItem(key, v); } catch (e) { /* ignore */ } };
  const show = () => {
    for (const r of document.querySelectorAll('input[name="tm-mode"]')) r.checked = r.value === mode();
    for (const r of document.querySelectorAll('input[name="tm-palette"], input[data-palette-pick]')) r.checked = r.value === palette();
    if (!menu) return;
    const btn = menu.querySelector("[data-theme-toggle]");
    const scheme = mode() === "system" ? names.system + " · " + (dark.matches ? "dark" : "light") : names[mode()];
    const pal = document.querySelector('input[name="tm-palette"]:checked');
    btn.title = "Theme: " + scheme + (pal ? ", " + pal.closest("label").querySelector(".tm-name").firstChild.textContent : "");
    for (const icon of btn.querySelectorAll("[data-icon]")) icon.toggleAttribute("hidden", icon.dataset.icon !== mode()); // SVG: an attribute, not .hidden
  };
  document.addEventListener("change", (e) => {
    const r = e.target;
    if (r.name === "tm-mode") {
      if (r.value === "system") delete root.dataset.theme; else root.dataset.theme = r.value;
      save("huntdeck-theme", r.value);
    } else if (r.name === "tm-palette" || r.matches("input[data-palette-pick]")) {
      if (r.value === "midnight") delete root.dataset.palette; else root.dataset.palette = r.value;
      save("huntdeck-palette", r.value);
    } else return;
    show();
  });
  dark.addEventListener("change", show);
  if (settings) settings.hidden = false;
  if (menu) {
    const btn = menu.querySelector("[data-theme-toggle]");
    const pop = menu.querySelector(".tm-pop");
    const open = (on) => {
      pop.hidden = !on;
      btn.setAttribute("aria-expanded", String(on));
      if (on) (pop.querySelector("input:checked") || pop.querySelector("input")).focus();
    };
    btn.addEventListener("click", () => open(pop.hidden));
    document.addEventListener("click", (e) => { if (!pop.hidden && !menu.contains(e.target)) open(false); });
    menu.addEventListener("keydown", (e) => {
      if (e.key === "Escape" && !pop.hidden) { open(false); btn.focus(); }
    });
    menu.addEventListener("focusout", (e) => { if (!pop.hidden && !menu.contains(e.relatedTarget) && e.relatedTarget) open(false); });
    menu.hidden = false;
  }
  show();
})();

// A read-only copy shows the user's own pages as a tour: every form that
// would change something is switched off (the server refuses them anyway).
if (document.documentElement.hasAttribute("data-read-only")) {
  for (const f of document.querySelectorAll('form[method="post" i]')) {
    if (f.action.endsWith("/api/v1/match")) continue; // only reads
    for (const el of f.elements) {
      el.disabled = true;
      el.title = "Not available in this demo";
    }
  }
}

// A read-only copy (a demo) is seen from anywhere, so its pages give times
// in UTC ("2026-11-05 14:00 UTC"): here they're put in the visitor's own
// zone, in text and tooltips alike. Without scripts they stay in UTC.
if (document.documentElement.hasAttribute("data-read-only")) {
  const utc = /(\d{4})-(\d\d)-(\d\d) (\d\d):(\d\d) UTC\b/g;
  const pad = (n) => String(n).padStart(2, "0");
  const zone = new Intl.DateTimeFormat(undefined, { timeZoneName: "short" });
  const local = (text) => text.replace(utc, (_, y, mo, d, h, mi) => {
    const t = new Date(Date.UTC(+y, +mo - 1, +d, +h, +mi));
    const name = zone.formatToParts(t).find((p) => p.type === "timeZoneName")?.value || "";
    return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${pad(t.getHours())}:${pad(t.getMinutes())} ${name}`.trim();
  });
  for (const el of document.querySelectorAll('[title*=" UTC"]')) el.title = local(el.title);
  const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) {
    if (n.nodeValue.includes(" UTC")) n.nodeValue = local(n.nodeValue);
  }
}

// A CSV link (data-csv-cols="table") takes the columns its table shows, in
// their order (columns.js may have hidden or moved some): ?cols=.
document.addEventListener("click", (e) => {
  const a = e.target.closest("a[data-csv-cols]");
  const row = a && document.querySelector(`table[data-cols="${a.dataset.csvCols}"] tbody tr`);
  if (!row) return;
  const keys = [...row.cells].filter(shown).map((td) => (td.className.match(/\bc-([a-z0-9]+)/) || [])[1])
    .filter((k) => k && k !== "prog" && k !== "check");
  const u = new URL(a.href, location.href);
  u.searchParams.set("cols", keys.join(","));
  a.href = u.pathname + u.search;
});

// A phone's search box can't show the whole hint: it gets a short one.
(() => {
  const box = document.getElementById("omni");
  if (!box) return;
  const long = box.placeholder;
  const narrow = matchMedia("(max-width: 700px)");
  const fit = () => { box.placeholder = narrow.matches ? "Search, or paste a host" : long; };
  fit();
  narrow.addEventListener("change", fit);
})();

// Phones: the Menu button drops the page list down; a click elsewhere,
// Escape or picking a page closes it.
(() => {
  const btn = document.querySelector("[data-nav-toggle]");
  if (!btn) return;
  const bar = btn.closest(".sidebar");
  const open = (on) => {
    bar.classList.toggle("nav-open", on);
    btn.setAttribute("aria-expanded", String(on));
  };
  btn.addEventListener("click", () => open(!bar.classList.contains("nav-open")));
  document.addEventListener("click", (e) => { if (!bar.contains(e.target)) open(false); });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && bar.classList.contains("nav-open")) { open(false); btn.focus(); }
  });
  btn.hidden = false;
})();

// A demo's banner hides for good in this browser (theme.js keeps it hidden).
(() => {
  const x = document.querySelector("[data-demo-banner-hide]");
  if (!x) return;
  x.addEventListener("click", () => {
    document.documentElement.dataset.demoBanner = "off";
    try { localStorage.setItem("huntdeck-demo-banner", "off"); } catch (e) { /* ignore */ }
  });
})();

// "/" focuses the top search bar, as on most hunting and code sites.
document.addEventListener("keydown", (e) => {
  if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target;
  if (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
  const box = document.getElementById("omni");
  if (!box) return;
  e.preventDefault();
  box.focus();
  box.select();
});

// Charts: instant tooltips, "Oct 31 · $38,300". Chart points and bars
// (g.pt, g.bar) carry a <title>, which browsers show only after a delay:
// its text moves to data-tip and shows at once by the pointer. Trend and
// month charts (.tchart) carry their points in data-days instead ("x|y|tip"
// joined by ";", in viewBox units; "x0~x1|y|tip" for a flat stretch), with a line and dot marking the one
// nearest the pointer. Without scripts the titles show as before.
(() => {
  const tip = document.createElement("div");
  tip.className = "ctip";
  tip.hidden = true;
  document.body.append(tip);
  const show = (text, e) => {
    tip.textContent = text;
    tip.hidden = false;
    const pad = 12, w = tip.offsetWidth, h = tip.offsetHeight;
    let left = e.clientX + pad, top = e.clientY + pad;
    if (left + w > window.innerWidth - 4) left = e.clientX - pad - w;
    if (top + h > window.innerHeight - 4) top = e.clientY - pad - h;
    tip.style.left = Math.max(4, left) + "px";
    tip.style.top = Math.max(4, top) + "px";
  };
  const hide = () => { tip.hidden = true; };

  for (const t of document.querySelectorAll("svg :is(g.pt, g.bar) > title")) {
    t.parentNode.dataset.tip = t.textContent;
    t.remove();
  }
  document.addEventListener("pointermove", (e) => {
    const g = e.target instanceof Element ? e.target.closest("svg [data-tip]") : null;
    if (g) show(g.dataset.tip, e);
    else if (!e.target.closest?.(".tchart[data-days]")) hide();
  });
  document.addEventListener("pointerleave", hide);

  const svgNS = "http://www.w3.org/2000/svg";
  for (const chart of document.querySelectorAll(".tchart[data-days]")) {
    const svg = chart.querySelector("svg");
    // A stretch of days with one value is "x0~x1|y|tip": the mark follows
    // the pointer along it.
    const days = chart.dataset.days.split(";").map((p) => {
      const [x, y, text] = p.split("|");
      const [x0, x1 = x0] = x.split("~").map(Number);
      return { x0, x1, y: Number(y), text };
    });
    if (!svg || days.length < 1 || (days.length < 2 && days[0].x0 === days[0].x1) ||
      days.some((d) => Number.isNaN(d.x0) || Number.isNaN(d.x1))) continue;
    const off = (d, x) => (x < d.x0 ? d.x0 - x : x > d.x1 ? x - d.x1 : 0);
    chart.removeAttribute("title"); // the tooltip below replaces it
    const vb = svg.viewBox.baseVal;
    const vw = (vb && vb.width) || 100, vh = (vb && vb.height) || 30;
    // The chart is stretched to its cell, so the dot is an ellipse that
    // looks round once stretched; the line keeps its width (CSS).
    const mark = document.createElementNS(svgNS, "line");
    mark.setAttribute("class", "cmark");
    mark.setAttribute("y1", "0");
    mark.setAttribute("y2", String(vh));
    const dot = document.createElementNS(svgNS, "ellipse");
    dot.setAttribute("class", "cdot");
    const marks = [mark, dot];
    marks.forEach((m) => { m.style.display = "none"; svg.append(m); });
    chart.addEventListener("pointermove", (e) => {
      const box = svg.getBoundingClientRect();
      if (!box.width) return;
      const x = ((e.clientX - box.left) / box.width) * vw;
      let day = days[0];
      for (const d of days) if (off(d, x) < off(day, x)) day = d;
      const at = Math.min(Math.max(x, day.x0), day.x1);
      mark.setAttribute("x1", at);
      mark.setAttribute("x2", at);
      dot.setAttribute("cx", at);
      dot.setAttribute("cy", day.y);
      dot.setAttribute("rx", (3 * vw) / box.width);
      dot.setAttribute("ry", (3 * vh) / box.height);
      marks.forEach((m) => { m.style.display = ""; });
      show(day.text, e);
    });
    chart.addEventListener("pointerleave", () => {
      hide();
      marks.forEach((m) => { m.style.display = "none"; });
    });
  }
})();

// The filter rail's Filters button (narrower screens) folds its groups
// away or shows them, and the choice stays for the next page (theme.js
// applies it before first paint).
for (const btn of document.querySelectorAll("[data-filters-toggle]")) {
  const root = document.documentElement;
  const sync = () => btn.setAttribute("aria-expanded", String(root.dataset.filters === "open"));
  sync();
  btn.addEventListener("click", () => {
    const open = root.dataset.filters !== "open";
    if (open) root.dataset.filters = "open";
    else delete root.dataset.filters;
    try {
      if (open) localStorage.setItem("huntdeck-filters", "open");
      else localStorage.removeItem("huntdeck-filters");
    } catch (e) { /* storage blocked: for this page only */ }
    sync();
  });
}

// Selects with data-autosubmit apply at once; their form's button is only
// needed without scripts.
for (const sel of document.querySelectorAll("select[data-autosubmit]")) {
  const btn = sel.form.querySelector("[data-autosubmit-button]");
  if (btn) btn.hidden = true;
  sel.addEventListener("change", () => sel.form.requestSubmit());
}

// Menus (details[data-menu]): a click outside or Escape closes them, and
// opening one closes the others.
(() => {
  const menus = () => document.querySelectorAll("details[data-menu][open]");
  document.addEventListener("click", (e) => {
    for (const m of menus()) if (!m.contains(e.target)) m.open = false;
  });
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    for (const m of menus()) { m.open = false; m.querySelector("summary")?.focus(); }
  });
})();

// A menu (details.tb-menu's .menu) opening near the screen's edge, as on
// a phone where the buttons wrap, is moved sideways to stay on screen.
document.addEventListener("toggle", (e) => {
  const d = e.target;
  if (!(d instanceof HTMLDetailsElement) || !d.matches(".tb-menu")) return;
  const m = d.querySelector(":scope > .menu");
  if (!m) return;
  m.style.translate = "";
  if (!d.open) return;
  const r = m.getBoundingClientRect(), edge = 8, vw = document.documentElement.clientWidth;
  const shift = r.left < edge ? edge - r.left : r.right > vw - edge ? vw - edge - r.right : 0;
  if (shift) m.style.translate = `${Math.round(shift)}px 0`;
}, true);

// Filter boxes (input[data-filter-rows="#table"]) hide the table's rows
// that don't contain what's typed, as it's typed; pressing Enter still
// asks the server, which searches every row, not only the ones shown.
for (const input of document.querySelectorAll("input[data-filter-rows]")) {
  const table = document.querySelector(input.dataset.filterRows);
  if (!table) continue;
  const rows = [...table.tBodies].flatMap((b) => [...b.rows]);
  input.addEventListener("input", () => {
    const q = input.value.trim().toLowerCase();
    for (const tr of rows) tr.hidden = q !== "" && !tr.textContent.toLowerCase().includes(q);
  });
}

// Contents lists (data-toc): the link to the section being read is marked
// as it scrolls by.
for (const toc of document.querySelectorAll("[data-toc]")) {
  const links = [...toc.querySelectorAll('a[href^="#"]')];
  const targets = links.map((a) => document.getElementById(decodeURIComponent(a.hash.slice(1)))).filter(Boolean);
  if (!targets.length) continue;
  const mark = () => {
    const line = 96; // just under the top bar
    let current = targets[0];
    for (const t of targets) if (t.getBoundingClientRect().top <= line) current = t;
    for (const a of links) {
      const on = a.hash.slice(1) === current.id;
      a.classList.toggle("on", on);
      if (on && toc.scrollHeight > toc.clientHeight) {
        const r = a.getBoundingClientRect(), box = toc.getBoundingClientRect();
        if (r.top < box.top || r.bottom > box.bottom) toc.scrollTop += r.top - box.top - box.height / 3;
      }
    }
  };
  let queued = false;
  window.addEventListener("scroll", () => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(() => { queued = false; mark(); });
  }, { passive: true });
  mark();
}

// Rows with data-href open it when clicked anywhere other than a link,
// button or field of their own, so a list's whole row is the target while
// tooltips and the row's other links keep working (as does a data-norow
// cell, such as a checkbox's). A middle, Ctrl or Cmd
// click opens a new tab, as a link would; data-ext rows (another site) always
// do, without sending this page as the referrer. Selecting text doesn't
// count as a click.
function rowLink(e) {
  const row = e.target.closest("tr[data-href], .nrow[data-href]");
  if (!row || e.target.closest("a, button, input, select, textarea, summary, label, [data-norow]")) return;
  if (row.closest(".board.editing")) return; // arranging the hunt board
  if (e.type === "click" && e.button !== 0) return;
  if (e.type === "auxclick" && e.button !== 1) return;
  if (String(getSelection())) return;
  const url = row.dataset.href;
  if ("ext" in row.dataset || e.type === "auxclick" || e.ctrlKey || e.metaKey) {
    window.open(url, "_blank", "noopener,noreferrer");
  } else {
    location.href = url;
  }
}
document.addEventListener("click", rowLink);
document.addEventListener("auxclick", rowLink);

// Live panels (data-live-refresh="seconds"), such as the poll history
// while a poll runs: the panel is read again from the page every so often
// while the tab is visible, until nothing in it is running.
for (const panel of document.querySelectorAll("[data-live-refresh]")) {
  const every = Math.max(5, Number(panel.dataset.liveRefresh) || 10) * 1000;
  let current = panel;
  const tick = async () => {
    if (document.hidden) return setTimeout(tick, every);
    try {
      const res = await fetch(location.href, { headers: { Accept: "text/html" }, cache: "no-store" });
      if (!res.ok) return setTimeout(tick, every * 3);
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      const next = current.id && doc.getElementById(current.id);
      if (!next) return;
      current.replaceWith(next);
      current = next;
      if (next.hasAttribute("data-live-refresh")) setTimeout(tick, every);
    } catch { setTimeout(tick, every * 3); }
  };
  setTimeout(tick, every);
}
