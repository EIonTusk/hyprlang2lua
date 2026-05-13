(async () => {
  const $ = (id) => document.getElementById(id);
  const input = $("input"), output = $("output"),
        outEmpty = $("out-empty"),
        emptyIcon = $("empty-icon"),
        emptyTitle = $("empty-title"), emptyHelp = $("empty-help"),
        emptySample = $("empty-sample"),
        inEmpty = $("in-empty"),
        uploadBtn = $("upload-btn"), fileInput = $("file-input"),
        notesWrap = $("notes-wrap"),
        errorBanner = $("error-banner"),
        copyBtn = $("copy-btn"), downloadBtn = $("download-btn"),
        sampleBtn = $("sample-btn"), clearBtn = $("clear-btn"),
        mergeConfig = $("merge-config"), stripComments = $("strip-comments"),
        hoistVariables = $("hoist-variables"),
        gutter = $("gutter"), gutterLines = $("gutter-lines"),
        outGutter = $("out-gutter"), outGutterLines = $("out-gutter-lines"),
        divider = $("divider"), main = document.querySelector("main"),
        toast = $("toast"), toastText = $("toast-text"),
        covSegOk = $("cov-seg-ok"), covSegFl = $("cov-seg-fl"),
        covPct = $("cov-pct"),
        statTrans = $("stat-trans"), statPass = $("stat-pass"), statFlag = $("stat-flag");

  // A small representative config: showcases variables, sections, nesting,
  // binds, and a few directives that exercise translated / passthrough paths.
  const SAMPLE = `# Sample hyprland.conf — variables, monitor, sections, binds.

$mainMod = SUPER
$terminal = kitty

monitor = ,preferred,auto,1

general {
    gaps_in = 5
    gaps_out = 20
    border_size = 2
    layout = dwindle
    allow_tearing = false
}

decoration {
    rounding = 10
    active_opacity = 1.0
    inactive_opacity = 0.9
    blur {
        enabled = true
        size = 3
        passes = 1
    }
}

input {
    kb_layout = us
    follow_mouse = 1
    touchpad {
        natural_scroll = false
    }
}

exec-once = waybar
env = XCURSOR_SIZE,24

bind = $mainMod, Q, exec, $terminal
bind = $mainMod, C, killactive
bind = $mainMod, V, togglefloating
bind = $mainMod, left, movefocus, l
bind = $mainMod, right, movefocus, r
bind = $mainMod, 1, workspace, 1
bind = $mainMod SHIFT, 1, movetoworkspace, 1
bindm = $mainMod, mouse:272, movewindow
`;

  // ===== Status / empty-state helpers ======================================
  const ICON_LOADING = '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="2" x2="12" y2="6"/><line x1="12" y1="18" x2="12" y2="22"/><line x1="4.93" y1="4.93" x2="7.76" y2="7.76"/><line x1="16.24" y1="16.24" x2="19.07" y2="19.07"/><line x1="2" y1="12" x2="6" y2="12"/><line x1="18" y1="12" x2="22" y2="12"/><line x1="4.93" y1="19.07" x2="7.76" y2="16.24"/><line x1="16.24" y1="7.76" x2="19.07" y2="4.93"/></svg>';
  const ICON_READY   = '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>';
  const ICON_ERROR   = '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="13"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>';

  function setEmptyState(kind, title, help, opts = {}) {
    // kind: 'loading' | 'idle' | 'error' | 'hidden'
    outEmpty.classList.remove("busy", "error");
    if (kind === "hidden") {
      outEmpty.style.display = "none";
      return;
    }
    outEmpty.style.display = "";
    if (kind === "loading") {
      outEmpty.classList.add("busy");
      emptyIcon.innerHTML = ICON_LOADING;
    } else if (kind === "error") {
      outEmpty.classList.add("error");
      emptyIcon.innerHTML = ICON_ERROR;
    } else {
      emptyIcon.innerHTML = ICON_READY;
    }
    emptyTitle.textContent = title;
    emptyHelp.textContent = help;
    emptySample.hidden = !opts.showSample;
  }

  function showToast(msg) {
    toastText.textContent = msg;
    toast.classList.add("show");
    clearTimeout(showToast._t);
    showToast._t = setTimeout(() => toast.classList.remove("show"), 1600);
  }

  // Initial state: wasm loading.
  setEmptyState("loading", "Loading converter…", "The Hyprland-to-Lua converter is starting up.");

  // ===== WASM load =========================================================
  const go = new Go();
  try {
    const resp = await fetch("main.wasm");
    if (!resp.ok) throw new Error(`fetch main.wasm: ${resp.status}`);
    const { instance } = await WebAssembly.instantiateStreaming(resp, go.importObject);
    go.run(instance);
  } catch (err) {
    setEmptyState("error", "Converter failed to load", err.message || String(err));
    return;
  }
  setEmptyState("hidden");

  let lastLua = "";

  // ===== Conversion ========================================================
  function showOutput(hasOutput) {
    outGutter.style.display = hasOutput ? "" : "none";
    output.hidden = !hasOutput;
    outEmpty.style.display = hasOutput ? "none" : "";
  }

  const convert = () => {
    const src = input.value;
    clearBtn.disabled = !src;
    inEmpty.hidden = !!src;

    if (!window.hyprlang2lua) return;

    if (!src) {
      lastLua = "";
      showOutput(false);
      setEmptyState("hidden");
      renderGutter(src, {});
      renderOutGutter("");
      setStats({ translated: 0, passthrough: 0, flagged: 0, coverage: 0 });
      setNotes([]);
      copyBtn.disabled = true;
      downloadBtn.disabled = true;
      errorBanner.classList.remove("show");
      return;
    }

    const opts = {
      mergeCalls: !!mergeConfig.checked,
      stripComments: !!stripComments.checked,
      hoistVariables: !!hoistVariables.checked,
    };

    let r;
    try {
      r = window.hyprlang2lua.convert(src, opts);
    } catch (err) {
      // Defensive: a wasm panic / unexpected exception would otherwise
      // freeze the UI in whatever state it was in. Surface it like a
      // parse error and stay recoverable on the next keystroke.
      r = { error: "internal: " + (err && err.message ? err.message : String(err)) };
    }

    if (r.error) {
      // Single place that owns the failure UI. Always clear every piece
      // of stale state so the next successful pass can rebuild cleanly,
      // and so a parse error doesn't leave old flagged notes or stats
      // visible from the previous good run.
      errorBanner.textContent = "error: " + r.error;
      errorBanner.classList.add("show");
      setEmptyState("error", "Could not parse input", r.error);
      showOutput(false);
      lastLua = "";
      renderGutter(src, {});
      renderOutGutter("");
      setStats({ translated: 0, passthrough: 0, flagged: 0, coverage: 0 });
      setNotes([]);
      copyBtn.disabled = true;
      downloadBtn.disabled = true;
      return;
    }

    errorBanner.classList.remove("show");
    errorBanner.textContent = "";
    output.textContent = r.lua;
    showOutput(true);
    lastLua = r.lua;
    setStats(r);
    setNotes(r.notes || []);
    renderGutter(src, r.lineClass || {});
    renderOutGutter(r.lua);
    copyBtn.disabled = !lastLua;
    downloadBtn.disabled = !lastLua;
  };

  const setStats = (r) => {
    const t = r.translated || 0, p = r.passthrough || 0, f = r.flagged || 0;
    statTrans.textContent = t;
    statPass.textContent  = p;
    statFlag.textContent  = f;
    // The bar mirrors the same definition as CoveragePct on the Go side:
    // translated / (translated + flagged). Passthrough (comments, blanks,
    // variables) is intentionally not counted — it isn't a directive that
    // could have been translated. The two segments always fill the bar.
    const denom = t + f;
    if (denom > 0) {
      covSegOk.style.width = (100 * t / denom) + "%";
      covSegFl.style.width = (100 * f / denom) + "%";
      covPct.textContent = (r.coverage * 100).toFixed(1) + "%";
    } else {
      covSegOk.style.width = "0%";
      covSegFl.style.width = "0%";
      covPct.textContent = "—";
    }
  };

  const setNotes = (notesArr) => {
    if (!notesArr || !notesArr.length) {
      notesWrap.innerHTML = "";
      return;
    }
    const frag = document.createDocumentFragment();
    const head = document.createElement("div");
    head.className = "notes-head";
    head.textContent = `Notes (${notesArr.length})`;
    const hint = document.createElement("span");
    hint.className = "hint";
    hint.textContent = "click to jump to line";
    head.appendChild(hint);
    frag.appendChild(head);
    for (const n of notesArr) {
      const row = document.createElement("div");
      row.className = "note";
      row.tabIndex = 0;
      row.setAttribute("role", "button");
      row.setAttribute("aria-label", `Jump to line ${n.line}: ${n.text}`);
      const badge = document.createElement("span");
      badge.className = "ln-badge";
      badge.textContent = "line " + n.line;
      const text = document.createElement("span");
      text.className = "text";
      text.textContent = n.text;
      row.append(badge, text);
      row.addEventListener("click", () => jumpToLine(n.line));
      row.addEventListener("keydown", (e) => {
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); jumpToLine(n.line); }
      });
      frag.appendChild(row);
    }
    notesWrap.innerHTML = "";
    notesWrap.appendChild(frag);
  };

  function jumpToLine(line) {
    const lh = parseFloat(getComputedStyle(input).lineHeight) || 20;
    // Center the target line in the viewport when possible.
    const top = (line - 1) * lh - input.clientHeight / 2 + lh / 2;
    input.scrollTop = Math.max(0, top);
    // Move the caret to the start of the line for clarity.
    const lines = input.value.split("\n");
    let pos = 0;
    for (let i = 0; i < line - 1 && i < lines.length; i++) pos += lines[i].length + 1;
    input.focus({ preventScroll: true });
    input.setSelectionRange(pos, pos);
    syncGutterScroll();
  }

  // ===== Gutters ===========================================================
  function renderGutterInto(target, srcLines, lineClass) {
    const lineCount = srcLines;
    if (target.childElementCount !== lineCount) {
      target.innerHTML = "";
      const frag = document.createDocumentFragment();
      for (let i = 0; i < lineCount; i++) {
        const d = document.createElement("div");
        d.className = "ln";
        d.textContent = i + 1;
        frag.appendChild(d);
      }
      target.appendChild(frag);
    }
    if (lineClass) {
      const children = target.children;
      for (let i = 0; i < lineCount; i++) {
        const cls = lineClass[String(i + 1)] || "";
        children[i].className = cls ? "ln " + cls : "ln";
      }
    }
  }

  const renderGutter = (src, lineClass) => {
    renderGutterInto(gutterLines, src ? src.split("\n").length : 0, lineClass);
    syncGutterScroll();
  };
  const renderOutGutter = (lua) => {
    renderGutterInto(outGutterLines, lua ? lua.split("\n").length : 0, null);
    syncOutGutterScroll();
  };

  const syncGutterScroll = () => {
    gutterLines.style.transform = `translateY(${-input.scrollTop}px)`;
  };
  const syncOutGutterScroll = () => {
    outGutterLines.style.transform = `translateY(${-output.scrollTop}px)`;
  };
  input.addEventListener("scroll", syncGutterScroll, { passive: true });
  output.addEventListener("scroll", syncOutGutterScroll, { passive: true });

  // ===== Input handling ====================================================
  mergeConfig.addEventListener("change", convert);
  stripComments.addEventListener("change", convert);
  hoistVariables.addEventListener("change", convert);

  let debounceTimer;
  input.addEventListener("input", () => {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(convert, 150);
  });

  // Buttons
  const loadSample = () => {
    input.value = SAMPLE;
    convert();
    input.focus({ preventScroll: true });
    input.setSelectionRange(0, 0);
    input.scrollTop = 0;
  };
  sampleBtn.addEventListener("click", loadSample);
  emptySample.addEventListener("click", loadSample);

  clearBtn.addEventListener("click", () => {
    input.value = "";
    convert();
    input.focus();
  });

  // ===== File picker =======================================================
  uploadBtn.addEventListener("click", () => fileInput.click());
  fileInput.addEventListener("change", async () => {
    const file = fileInput.files && fileInput.files[0];
    if (!file) return;
    input.value = await file.text();
    fileInput.value = "";
    convert();
  });

  // ===== Settings popover dismiss ==========================================
  const settings = $("settings");
  document.addEventListener("pointerdown", (e) => {
    if (settings.open && !settings.contains(e.target)) settings.open = false;
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && settings.open) settings.open = false;
  });

  // ===== File drop =========================================================
  // Track depth so leaving a child element doesn't dismiss the overlay.
  let dragDepth = 0;
  const setFileDrag = (on) => document.body.classList.toggle("dragging-file", on);
  document.addEventListener("dragenter", (e) => {
    if (!e.dataTransfer || !Array.from(e.dataTransfer.types).includes("Files")) return;
    e.preventDefault();
    dragDepth++;
    setFileDrag(true);
  });
  document.addEventListener("dragover", (e) => {
    if (document.body.classList.contains("dragging-file")) e.preventDefault();
  });
  document.addEventListener("dragleave", () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) setFileDrag(false);
  });
  document.addEventListener("drop", async (e) => {
    if (!e.dataTransfer || !e.dataTransfer.files || !e.dataTransfer.files.length) {
      setFileDrag(false); dragDepth = 0; return;
    }
    e.preventDefault();
    dragDepth = 0;
    setFileDrag(false);
    const file = e.dataTransfer.files[0];
    input.value = await file.text();
    convert();
  });

  // ===== Copy / Download ===================================================
  copyBtn.addEventListener("click", async () => {
    if (!lastLua) return;
    try {
      await navigator.clipboard.writeText(lastLua);
      showToast("Copied to clipboard");
    } catch (err) {
      // Fallback: select the output so the user can ctrl+c manually.
      const sel = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(output);
      sel.removeAllRanges(); sel.addRange(range);
      showToast("Selected output — press Ctrl+C to copy");
    }
  });

  downloadBtn.addEventListener("click", () => {
    if (!lastLua) return;
    const blob = new Blob([lastLua], { type: "text/plain" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = "hyprland.lua";
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(a.href);
  });

  // ===== Keyboard shortcuts ================================================
  document.addEventListener("keydown", (e) => {
    const mod = e.ctrlKey || e.metaKey;
    if (!mod) return;
    if (e.key.toLowerCase() === "s" && lastLua) {
      e.preventDefault();
      downloadBtn.click();
    }
  });

  // ===== Draggable divider =================================================
  // Horizontal on wide viewports, vertical when the layout stacks.
  let dragging = false;
  const isStacked = () => window.matchMedia("(max-width: 720px)").matches;
  divider.addEventListener("mousedown", (e) => {
    dragging = true;
    divider.classList.add("dragging");
    document.body.classList.add("dragging-divider");
    document.body.style.cursor = isStacked() ? "row-resize" : "col-resize";
    e.preventDefault();
  });
  document.addEventListener("mousemove", (e) => {
    if (!dragging) return;
    const rect = main.getBoundingClientRect();
    const dividerSize = 6;
    if (isStacked()) {
      let top = e.clientY - rect.top;
      const min = 80, max = rect.height - dividerSize - 80;
      top = Math.max(min, Math.min(max, top));
      main.style.gridTemplateColumns = "1fr";
      main.style.gridTemplateRows = `${top}px ${dividerSize}px 1fr`;
    } else {
      let left = e.clientX - rect.left;
      const min = 160, max = rect.width - dividerSize - 160;
      left = Math.max(min, Math.min(max, left));
      main.style.setProperty("--split", `${left}px`);
      main.style.gridTemplateRows = "";
      main.style.gridTemplateColumns = "";
    }
  });
  document.addEventListener("mouseup", () => {
    if (!dragging) return;
    dragging = false;
    divider.classList.remove("dragging");
    document.body.classList.remove("dragging-divider");
    document.body.style.cursor = "";
  });

  // If somehow there's existing input (autofill), render it now.
  if (input.value) convert();
})();
