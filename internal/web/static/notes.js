// A hunter's notes on programs, kept in this browser only (localStorage,
// as the watchlist is): never sent to HuntDeck or anywhere else, so they
// need no account and work on a read-only copy such as the demo.
// - A program's Notes tab edits its note, saved as you type (debounced).
// - Programs with notes are marked beside their names wherever they're
//   listed, and the rail's "Has notes" keeps a list to them (?noted=).
// - Export downloads every note as JSON; Import adds an exported file's,
//   a note replacing this browser's only if it's newer.
// Notes are keyed by platform/handle, as program pages' addresses are.
(() => {
  const KEY = "huntdeck.notes", FORMAT = "huntdeck-notes", MAX = 100000, DELAY = 600;
  const keyOK = (k) => typeof k === "string" && /^[a-z0-9][a-z0-9-]{0,39}\/[^/?#\s]{1,200}$/.test(k);
  const read = () => {
    try {
      const v = JSON.parse(localStorage.getItem(KEY));
      return v && typeof v === "object" && !Array.isArray(v) ? v : {};
    } catch { return {}; }
  };
  const write = (all) => {
    try { localStorage.setItem(KEY, JSON.stringify(all)); return true; } catch { return false; }
  };
  const has = (n) => n && typeof n.text === "string" && n.text.trim() !== "";

  // Marks and links, from the notes as stored now.
  const show = () => {
    const all = read();
    const keys = Object.keys(all).filter((k) => keyOK(k) && has(all[k])).sort();
    const paths = new Set(keys.map((k) => "/programs/" + k));
    for (const a of document.querySelectorAll("main .pn > a.name")) {
      const on = paths.has(new URL(a.href).pathname);
      a.parentElement.classList.toggle("noted", on);
    }
    for (const tab of document.querySelectorAll("[data-notes-tab]")) {
      tab.classList.toggle("has-notes", keys.includes(tab.dataset.notesTab));
    }
    for (const a of document.querySelectorAll("[data-notes-link]")) {
      const on = a.dataset.notesLink === "on";
      if (!on) {
        const u = new URL(location.href);
        u.searchParams.delete("page");
        u.searchParams.set("noted", keys.join(","));
        a.href = u.pathname + u.search;
        a.hidden = keys.length === 0;
      }
      const n = on ? (new URL(location.href).searchParams.get("noted") || "").split(",").filter(Boolean).length : keys.length;
      const em = a.querySelector("em");
      if (em) em.textContent = n ? String(n) : "";
      const group = a.closest("[data-watch-group]"); // shared with the watchlist (watch.js)
      if (group) group.hidden = [...group.querySelectorAll("a")].every((x) => x.hidden);
    }
    for (const a of document.querySelectorAll("[data-notes-all]")) {
      a.href = "/programs?state=all&noted=" + encodeURIComponent(keys.join(","));
      a.hidden = keys.length === 0;
    }
  };

  // The Notes tab.
  const box = document.querySelector("[data-notes]");
  const text = box?.querySelector("textarea");
  const status = box?.querySelector("[data-notes-status]");
  const meta = box?.querySelector("[data-notes-meta]");
  const key = box?.dataset.notes;
  const say = (msg, bad) => {
    if (!status) return;
    status.textContent = msg;
    status.classList.toggle("bad", !!bad);
  };
  const showMeta = (n) => {
    if (!meta) return;
    meta.textContent = has(n) && n.updated ? "Last edited " + new Date(n.updated).toLocaleString() : "";
  };
  let timer = 0, dirty = false;
  const save = () => {
    clearTimeout(timer);
    if (!dirty || !text) return;
    const all = read();
    const value = text.value.slice(0, MAX);
    if (value.trim() === "") delete all[key];
    else all[key] = { text: value, updated: new Date().toISOString(), name: box.dataset.notesName || "" };
    if (write(all)) {
      dirty = false;
      say(value.trim() === "" ? "Empty: nothing kept" : "Saved");
      showMeta(all[key]);
      show();
    } else {
      say("Not saved: this browser's storage is full or turned off", true);
    }
  };
  if (text && keyOK(key)) {
    const n = read()[key];
    text.value = has(n) ? n.text : "";
    text.disabled = false;
    showMeta(n);
    say(has(n) ? "Saved" : "");
    text.addEventListener("input", () => {
      dirty = true;
      say("Saving…");
      clearTimeout(timer);
      timer = setTimeout(save, DELAY);
    });
    text.addEventListener("blur", save);
    text.addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); save(); } // the reflex, kept
      if (e.key === "Escape") text.blur();
    });
    window.addEventListener("pagehide", save);
    // Another tab edited it: take theirs unless this one has unsaved typing.
    window.addEventListener("storage", (e) => {
      if (e.key !== KEY) return;
      show();
      if (!dirty) {
        const m = read()[key];
        text.value = has(m) ? m.text : "";
        showMeta(m);
      }
    });
    if (!location.hash) text.focus();
  } else {
    window.addEventListener("storage", (e) => { if (e.key === KEY) show(); });
  }

  // Export and import.
  box?.querySelector("[data-notes-export]")?.addEventListener("click", () => {
    save();
    const all = read();
    const notes = {};
    for (const k of Object.keys(all).sort()) if (keyOK(k) && has(all[k])) notes[k] = all[k];
    const blob = new Blob([JSON.stringify({ format: FORMAT, version: 1, exported: new Date().toISOString(), notes }, null, 2) + "\n"],
      { type: "application/json" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "huntdeck-notes-" + new Date().toISOString().slice(0, 10) + ".json";
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
    say(`Exported ${Object.keys(notes).length} ${Object.keys(notes).length === 1 ? "note" : "notes"}`);
  });
  const file = box?.querySelector("[data-notes-file]");
  box?.querySelector("[data-notes-import]")?.addEventListener("click", () => file?.click());
  file?.addEventListener("change", async () => {
    const f = file.files?.[0];
    file.value = "";
    if (!f) return;
    let doc;
    try {
      if (f.size > 20 * 1024 * 1024) throw new Error("too big");
      doc = JSON.parse(await f.text());
      if (doc?.format !== FORMAT || typeof doc.notes !== "object" || !doc.notes) throw new Error("not a notes file");
    } catch {
      say("Not imported: that isn't a HuntDeck notes export", true);
      return;
    }
    save();
    const all = read();
    let added = 0, kept = 0, bad = 0;
    for (const [k, n] of Object.entries(doc.notes)) {
      if (!keyOK(k) || !has(n) || n.text.length > MAX) { bad++; continue; }
      const mine = all[k];
      const theirs = { text: n.text, updated: typeof n.updated === "string" && !isNaN(Date.parse(n.updated)) ? n.updated : new Date(0).toISOString(),
        name: typeof n.name === "string" ? n.name.slice(0, 300) : "" };
      if (has(mine) && Date.parse(mine.updated || 0) >= Date.parse(theirs.updated)) { kept++; continue; }
      all[k] = theirs;
      added++;
    }
    if (!write(all)) { say("Not imported: this browser's storage is full or turned off", true); return; }
    say(`Imported ${added}${kept ? `, kept ${kept} newer here` : ""}${bad ? `, skipped ${bad} unreadable` : ""}`);
    if (text && !dirty) {
      const m = all[key];
      text.value = has(m) ? m.text : "";
      showMeta(m);
    }
    show();
  });

  show();
})();
