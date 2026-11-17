(() => {
  const state = {
    lib: null,
    current: "",
    nodes: new Map(),
    versions: new Map(),
    expanded: new Set(),
    inc: [],
    exc: [],
    status: {},
    filter: "",
    mounts: [],
    active: "",
    view: localStorage.getItem("syncscope-view") || "list",
    theme: localStorage.getItem("syncscope-theme") || "light",
    busy: false,
  };

  const $ = (s) => document.querySelector(s);
  const isAncestor = (a, b) => b.startsWith(a + "/");
  const covered = (p, roots) => roots.some((r) => p === r || isAncestor(r, p));
  const hasDesc = (p, roots) => roots.some((r) => isAncestor(p, r));
  const selected = (p) => covered(p, state.inc) && !covered(p, state.exc);
  const partial = (p) => !covered(p, state.exc) && !selected(p) && hasDesc(p, state.inc);

  const svg = (body) => `<svg viewBox="0 0 24 24" aria-hidden="true">${body}</svg>`;
  const ICONS = {
    folder: svg('<path d="M3.5 7.5A2.5 2.5 0 0 1 6 5h4l2 2h6a2.5 2.5 0 0 1 2.5 2.5v7A2.5 2.5 0 0 1 18 19H6a2.5 2.5 0 0 1-2.5-2.5v-9Z"/>'),
    file: svg('<path d="M7 3.5h7l4 4V20a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z"/><path d="M14 3.5V8h4"/>'),
    image: svg('<rect x="3.5" y="4.5" width="17" height="15" rx="2"/><circle cx="9" cy="10" r="1.6"/><path d="m4 17 5-5 4 4 2-2 5 4"/>'),
    video: svg('<rect x="3.5" y="5" width="17" height="14" rx="2"/><path d="m10 9 5 3-5 3V9Z"/>'),
    audio: svg('<path d="M9 18V6l9-2v12"/><circle cx="7" cy="18" r="2"/><circle cx="16" cy="16" r="2"/>'),
    pdf: svg('<path d="M7 3.5h7l4 4V20a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z"/><path d="M14 3.5V8h4M8 16h8M8 12h4"/>'),
    code: svg('<path d="m8 8-4 4 4 4M16 8l4 4-4 4M13 5l-2 14"/>'),
    archive: svg('<rect x="4" y="3.5" width="16" height="17" rx="2"/><path d="M12 3.5v6M10 6h4M10 9h4M10 12h4"/>'),
    text: svg('<path d="M7 3.5h7l4 4V20a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4.5a1 1 0 0 1 1-1Z"/><path d="M14 3.5V8h4M8 12h8M8 15h8M8 18h5"/>'),
    chevron: svg('<path d="m9 6 6 6-6 6"/>'),
    download: svg('<path d="M12 4v11M8 11l4 4 4-4M5 20h14"/>'),
  };

  function ext(name) {
    const i = name.lastIndexOf(".");
    return i >= 0 ? name.slice(i + 1).toLowerCase() : "";
  }
  function kind(node) {
    if (node.is_dir) return "folder";
    const e = ext(node.name);
    if (["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "avif", "heic"].includes(e)) return "image";
    if (["mp4", "webm", "mov", "mkv", "avi", "m4v"].includes(e)) return "video";
    if (["mp3", "flac", "wav", "ogg", "m4a", "aac", "opus"].includes(e)) return "audio";
    if (e === "pdf") return "pdf";
    if (["zip", "rar", "7z", "tar", "gz", "bz2", "xz", "iso"].includes(e)) return "archive";
    if (["js", "ts", "tsx", "jsx", "go", "py", "rs", "java", "c", "h", "cpp", "css", "scss", "html", "json", "yml", "yaml", "sh", "toml", "sql", "xml"].includes(e)) return "code";
    if (["txt", "md", "log", "csv", "ini", "conf", "env", "rst"].includes(e)) return "text";
    return "file";
  }
  const icon = (node) => ICONS[kind(node)] || ICONS.file;

  function fmtSize(n) {
    if (n === undefined || n === null) return "—";
    if (n < 1024) return `${n} Б`;
    const u = ["КБ", "МБ", "ГБ", "ТБ"];
    let v = n / 1024, i = 0;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(v >= 10 ? 0 : 1)} ${u[i]}`;
  }
  function fmtDate(s) {
    if (!s) return "—";
    const d = new Date(s);
    if (Number.isNaN(d.getTime())) return "—";
    return d.toLocaleString("ru-RU", { day: "2-digit", month: "2-digit", year: "2-digit", hour: "2-digit", minute: "2-digit" });
  }
  const q = (p) => encodeURIComponent(p);
  const mq = () => (state.active ? `&mount=${q(state.active)}` : "");
  const api = async (path, opts = {}) => {
    const res = await fetch(path, { headers: { "Content-Type": "application/json" }, ...opts });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
    return data;
  };

  async function ensureLoaded(path, force = false) {
    if (!force && state.nodes.has(path)) return;
    const page = await api(`api/catalog?parent=${q(path)}${mq()}`);
    state.nodes.set(path, page.nodes || []);
    state.versions.set(path, page.version || 0);
  }
  function ancestors(path) {
    const parts = path.split("/").filter(Boolean);
    const out = [""];
    for (let i = 1; i <= parts.length; i++) out.push(parts.slice(0, i).join("/"));
    return out;
  }
  async function navigate(path, force = false) {
    state.current = path;
    for (const a of ancestors(path)) state.expanded.add(a);
    await ensureLoaded(path, force);
    if (path) await ensureLoaded(path.split("/").slice(0, -1).join("/"), force);
    const params = new URLSearchParams(location.search);
    if (path) params.set("path", path);
    else params.delete("path");
    if (state.active) params.set("mount", state.active);
    if (state.view !== "list") params.set("view", state.view);
    else params.delete("view");
    history.replaceState(null, "", `${location.pathname}${params.toString() ? "?" + params : ""}`);
    render();
  }

  function statusFor(node) {
    if (node.is_dir && partial(node.path)) return { text: "частично", cls: "info" };
    if (!selected(node.path)) {
      return covered(node.path, state.exc) ? { text: "исключено", cls: "" } : { text: "на сервере", cls: "" };
    }
    const st = state.status || {};
    if (st.errors > 0) return { text: "ошибка", cls: "bad" };
    if ((st.state && st.state !== "idle") || st.need_files > 0) return { text: "синхронизация", cls: "warn" };
    return { text: "синхронизировано", cls: "ok" };
  }

  function checkbox(node, cls = "cb") {
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.className = cls;
    cb.disabled = !node.is_dir;
    if (!node.is_dir) cb.title = "Выбор только на уровне папки";
    if (node.is_dir) {
      const sel = selected(node.path);
      cb.checked = sel;
      cb.indeterminate = !sel && partial(node.path);
      cb.addEventListener("click", (e) => e.stopPropagation());
      cb.addEventListener("change", () => toggleDir(node.path, cb.checked));
    }
    return cb;
  }

  async function toggleDir(path, want) {
    if (state.busy) return;
    let inc = [...state.inc];
    let exc = [...state.exc];
    if (want) {
      inc = inc.filter((x) => !(x === path || isAncestor(path, x)));
      exc = exc.filter((x) => !(x === path || isAncestor(path, x) || isAncestor(x, path)));
      if (!inc.includes(path)) inc.push(path);
    } else {
      inc = inc.filter((x) => !(x === path || isAncestor(path, x)));
      exc = exc.filter((x) => !(x === path || isAncestor(path, x)));
      if (covered(path, inc)) exc.push(path);
    }
    state.busy = true;
    try {
      const data = await api("api/selection", { method: "POST", body: JSON.stringify({ mount: state.active, included: inc, excluded: exc }) });
      state.inc = data.selection.included || [];
      state.exc = data.selection.excluded || [];
      await refreshStatus();
      render();
    } catch (e) {
      alert("Не удалось применить выбор: " + e.message);
    } finally {
      state.busy = false;
    }
  }

  async function shareNode(node) {
    const password = prompt("Пароль для ссылки (необязательно):", "");
    if (password === null) return;
    const daysRaw = prompt("Срок действия, дней:", "7");
    if (daysRaw === null) return;
    const days = Math.max(1, Math.min(30, parseInt(daysRaw, 10) || 7));
    try {
      const link = await api("api/share", {
        method: "POST",
        body: JSON.stringify({
          mount: state.active,
          path: node.path,
          is_dir: node.is_dir,
          expires_in: days * 86400,
          password,
        }),
      });
      try {
        await navigator.clipboard.writeText(link.url);
        alert("Ссылка скопирована:\n" + link.url);
      } catch (_) {
        prompt("Ссылка:", link.url);
      }
    } catch (e) {
      alert("Не удалось создать ссылку: " + e.message);
    }
  }

  async function freeSpace(path) {
    if (!confirm(`Удалить локальную копию «${path}»? На сервере файлы останутся.`)) return;
    try {
      await api("api/free-space", { method: "POST", body: JSON.stringify({ mount: state.active, path }) });
      const sel = await api(`api/selection?mount=${q(state.active)}`);
      state.inc = sel.included || [];
      state.exc = sel.excluded || [];
      await refreshStatus();
      await ensureLoaded(state.current, true);
      render();
    } catch (e) {
      alert("Не удалось освободить место: " + e.message);
    }
  }

  function renderCrumbs() {
    const el = $("#crumbs");
    el.textContent = "";
    const root = document.createElement("button");
    root.textContent = "Корень";
    root.onclick = () => navigate("");
    el.appendChild(root);
    let acc = "";
    for (const part of state.current.split("/").filter(Boolean)) {
      acc = acc ? `${acc}/${part}` : part;
      const sep = document.createElement("span");
      sep.className = "sep";
      sep.textContent = "/";
      el.appendChild(sep);
      const b = document.createElement("button");
      b.textContent = part;
      const target = acc;
      b.onclick = () => navigate(target);
      el.appendChild(b);
    }
  }

  function renderTree() {
    const root = $("#tree");
    root.textContent = "";
    const nodes = state.nodes.get("");
    if (!nodes) {
      root.innerHTML = '<div class="muted">Загрузка…</div>';
      return;
    }
    if (!nodes.length) {
      root.innerHTML = '<div class="muted">Пусто</div>';
      return;
    }
    const ul = document.createElement("ul");
    const level = (list, container, depth) => {
      for (const node of list) {
        if (!node.is_dir) continue;
        const li = document.createElement("li");
        const row = document.createElement("div");
        row.className = "tree-row" + (state.current === node.path ? " active" : "");
        row.style.paddingLeft = `${depth * 12 + 4}px`;

        const chev = document.createElement("span");
        chev.className = "chev";
        chev.innerHTML = ICONS.chevron;
        if (state.expanded.has(node.path)) chev.style.transform = "rotate(90deg)";
        chev.onclick = async (e) => {
          e.stopPropagation();
          if (state.expanded.has(node.path)) state.expanded.delete(node.path);
          else {
            state.expanded.add(node.path);
            await ensureLoaded(node.path);
          }
          renderTree();
        };

        const label = document.createElement("span");
        label.className = "label";
        label.textContent = node.name;
        label.title = node.path;
        label.onclick = () => navigate(node.path);

        const cb = checkbox(node, "tc");
        row.append(chev, label, cb);
        li.appendChild(row);

        if (state.expanded.has(node.path)) {
          const kids = state.nodes.get(node.path);
          if (kids) {
            const sub = document.createElement("ul");
            level(kids, sub, depth + 1);
            li.appendChild(sub);
          } else {
            ensureLoaded(node.path).then(renderTree).catch(() => {});
          }
        }
        container.appendChild(li);
      }
    };
    level(nodes, ul, 0);
    root.appendChild(ul);
  }

  function statusBadge(node) {
    const st = statusFor(node);
    const b = document.createElement("span");
    b.className = "badge " + st.cls;
    b.textContent = st.text;
    return b;
  }

  function openNode(node) {
    if (node.is_dir) navigate(node.path);
    else openPreview(node);
  }

  function renderList(nodes) {
    const wrap = document.createElement("div");
    wrap.className = "list";
    const head = document.createElement("div");
    head.className = "list-head";
    head.innerHTML = "<span></span><span>Имя</span><span>Размер</span><span>Изменён</span><span>Статус</span><span></span>";
    wrap.appendChild(head);

    for (const node of nodes) {
      const row = document.createElement("div");
      row.className = "row";

      const cbcell = document.createElement("div");
      cbcell.appendChild(checkbox(node));
      const name = document.createElement("div");
      name.className = "name";
      name.onclick = () => openNode(node);
      const ic = document.createElement("span");
      ic.style.cssText = "width:22px;height:22px;color:var(--muted);flex:0 0 auto";
      ic.innerHTML = icon(node);
      const lab = document.createElement("span");
      lab.className = "label";
      lab.textContent = node.name;
      lab.title = node.path;
      name.append(ic, lab);

      const size = document.createElement("div");
      size.className = "size";
      size.textContent = node.is_dir ? "—" : fmtSize(node.size);
      const date = document.createElement("div");
      date.className = "date";
      date.textContent = fmtDate(node.mod_time);
      const st = document.createElement("div");
      st.appendChild(statusBadge(node));
      const actions = document.createElement("div");
      actions.className = "actions";
      const share = document.createElement("button");
      share.className = "mini";
      share.textContent = "Ссылка";
      share.title = "Публичная ссылка";
      share.onclick = (e) => { e.stopPropagation(); shareNode(node); };
      actions.appendChild(share);
      if (node.is_dir && selected(node.path)) {
        const b = document.createElement("button");
        b.className = "mini";
        b.style.marginLeft = "4px";
        b.textContent = "Освободить";
        b.onclick = (e) => { e.stopPropagation(); freeSpace(node.path); };
        actions.appendChild(b);
      }
      row.append(cbcell, name, size, date, st, actions);
      wrap.appendChild(row);
    }
    return wrap;
  }

  function renderGrid(nodes) {
    const grid = document.createElement("div");
    grid.className = "grid";
    for (const node of nodes) {
      const card = document.createElement("div");
      card.className = "card";
      card.onclick = () => openNode(node);

      const ccb = document.createElement("div");
      ccb.className = "cardcheck";
      ccb.appendChild(checkbox(node));
      const art = document.createElement("div");
      art.className = "art";
      if (kind(node) === "image") {
        const img = document.createElement("img");
        img.loading = "lazy";
        img.alt = "";
        img.src = `api/preview?path=${q(node.path)}${mq()}`;
        img.onerror = () => { art.innerHTML = `<span class="ic">${ICONS.image}</span>`; };
        art.appendChild(img);
      } else {
        art.innerHTML = `<span class="ic">${icon(node)}</span>`;
      }
      const nm = document.createElement("div");
      nm.className = "cname";
      nm.textContent = node.name;
      nm.title = node.path;
      const meta = document.createElement("div");
      meta.className = "cmeta";
      const sz = document.createElement("span");
      sz.textContent = node.is_dir ? "папка" : fmtSize(node.size);
      meta.append(sz, statusBadge(node));
      const share = document.createElement("button");
      share.className = "mini";
      share.textContent = "Ссылка";
      share.onclick = (e) => { e.stopPropagation(); shareNode(node); };
      meta.appendChild(share);
      if (node.is_dir && selected(node.path)) {
        const b = document.createElement("button");
        b.className = "mini";
        b.textContent = "Освободить";
        b.onclick = (e) => { e.stopPropagation(); freeSpace(node.path); };
        meta.appendChild(b);
      }
      card.append(ccb, art, nm, meta);
      grid.appendChild(card);
    }
    return grid;
  }

  function renderBrowser() {
    const browser = $("#browser");
    browser.textContent = "";
    const nodes = (state.nodes.get(state.current) || []).filter((n) => !state.filter || n.name.toLowerCase().includes(state.filter.toLowerCase()));
    if (!state.nodes.has(state.current)) {
      browser.innerHTML = '<div class="empty">Загрузка…</div>';
      return;
    }
    if (!nodes.length) {
      browser.innerHTML = `<div class="empty">${state.filter ? "Ничего не найдено" : "Папка пуста"}</div>`;
      return;
    }
    browser.appendChild(state.view === "grid" ? renderGrid(nodes) : renderList(nodes));
  }

  function renderMounts() {
    const el = $("#mounts");
    el.textContent = "";
    for (const m of state.mounts) {
      const b = document.createElement("button");
      b.className = "mount-item" + (m.id === state.active ? " active" : "");
      const ic = document.createElement("span");
      ic.style.cssText = "width:15px;height:15px;flex:0 0 auto";
      ic.innerHTML = ICONS.folder;
      const nm = document.createElement("span");
      nm.className = "mname";
      nm.textContent = m.name;
      nm.title = m.local;
      b.append(ic, nm);
      b.onclick = () => switchMount(m.id);
      el.appendChild(b);
    }
  }

  function renderHead() {
    const name = state.current ? state.current.split("/").pop() : (state.lib ? state.lib.name : "Корень");
    $("#foldertitle").textContent = name;
    const count = (state.nodes.get(state.current) || []).length;
    $("#foldersub").textContent = `${count} объект(ов) · ${state.current || "/"}`;
    const sel = state.inc.length;
    $("#summary").innerHTML = sel ? `Выбрано папок: <b>${sel}</b>` : "Ничего не выбрано";
    $("#sidesummary").textContent = sel ? String(sel) : "—";
    $("#view-list").classList.toggle("active", state.view === "list");
    $("#view-grid").classList.toggle("active", state.view === "grid");
  }

  function renderStatus() {
    const st = state.status || {};
    const conn = $("#conn");
    const relay = (st.connection_type || "").startsWith("relay");
    conn.textContent = st.connected ? (relay ? "relay" : "direct") : "офлайн";
    conn.className = "pill " + (st.connected ? (relay ? "warn" : "ok") : "bad");
    const sync = $("#sync");
    if (!st.connected) { sync.textContent = "нет связи"; sync.className = "pill bad"; }
    else if (st.errors > 0) { sync.textContent = "ошибка"; sync.className = "pill bad"; }
    else if (st.state !== "idle" || st.need_files > 0) { sync.textContent = "синхронизация"; sync.className = "pill warn"; }
    else { sync.textContent = "idle"; sync.className = "pill ok"; }
    $("#statusbar").textContent = [
      `state: ${st.state || "—"}`,
      `need: ${st.need_files ?? "—"}`,
      `local: ${st.local_files ?? "—"}`,
      st.connection_address ? `peer: ${st.connection_address}` : "",
      state.lib ? `path: ${state.lib.local}` : "",
    ].filter(Boolean).join(" · ");
  }

  function render() {
    renderMounts();
    renderCrumbs();
    renderTree();
    renderHead();
    renderBrowser();
    renderStatus();
  }

  async function refreshStatus() {
    try {
      state.status = await api(`api/status?mount=${q(state.active)}`);
    } catch (_) {
      state.status = { connected: false, state: "unknown" };
    }
    renderStatus();
  }

  function note(text, detail) {
    const d = document.createElement("div");
    d.className = "modal-note";
    const b = document.createElement("b");
    b.textContent = text;
    d.appendChild(b);
    if (detail) {
      const p = document.createElement("div");
      p.textContent = detail;
      d.appendChild(p);
    }
    return d;
  }

  function closePreview() {
    $("#preview").hidden = true;
    $("#preview-body").textContent = "";
  }

  async function openPreview(node) {
    const modal = $("#preview");
    modal.hidden = false;
    $("#preview-name").textContent = node.name;
    $("#preview-meta").textContent = `${fmtSize(node.size)} · ${fmtDate(node.mod_time)} · ${statusFor(node).text}`;
    $("#preview-icon").innerHTML = icon(node);
    $("#preview-icon").style.cssText = "width:26px;height:26px;color:var(--accent)";
    $("#preview-download").href = `api/download?path=${q(node.path)}${mq()}`;
    const body = $("#preview-body");
    body.textContent = "";
    const src = `api/preview?path=${q(node.path)}${mq()}`;
    const k = kind(node);
    if (k === "image") {
      const img = document.createElement("img");
      img.src = src;
      img.onerror = () => { body.textContent = ""; body.appendChild(note("Файл не скачан", "Он есть на сервере, но ещё не выбран для синхронизации.")); };
      body.appendChild(img);
    } else if (k === "video") {
      const v = document.createElement("video");
      v.controls = true;
      v.src = src;
      v.onerror = () => { body.textContent = ""; body.appendChild(note("Файл не скачан", "Сначала синхронизируйте папку.")); };
      body.appendChild(v);
    } else if (k === "audio") {
      const a = document.createElement("audio");
      a.controls = true;
      a.src = src;
      a.onerror = () => { body.textContent = ""; body.appendChild(note("Файл не скачан", "Сначала синхронизируйте папку.")); };
      body.appendChild(a);
    } else if (k === "pdf") {
      const f = document.createElement("iframe");
      f.src = src;
      body.appendChild(f);
    } else if (k === "text" || k === "code") {
      try {
        const res = await fetch(src);
        if (!res.ok) throw new Error("not local");
        const text = await res.text();
        const pre = document.createElement("pre");
        pre.textContent = text.length > 400000 ? text.slice(0, 400000) + "\n\n…" : text;
        body.appendChild(pre);
      } catch (_) {
        body.appendChild(note("Файл не скачан", "Он есть на сервере, но ещё не выбран для синхронизации."));
      }
    } else {
      body.appendChild(note("Предпросмотр недоступен", "Скачайте файл, чтобы открыть его в системном приложении."));
    }
  }

  async function poll() {
    if (document.hidden || state.busy) return;
    await refreshStatus();
    try {
      const page = await api(`api/catalog?parent=${q(state.current)}${mq()}`);
      if ((page.version || 0) !== (state.versions.get(state.current) || 0)) {
        state.nodes.set(state.current, page.nodes || []);
        state.versions.set(state.current, page.version || 0);
        render();
      }
    } catch (_) {}
  }

  async function switchMount(id) {
    if (id === state.active) return;
    state.active = id;
    state.current = "";
    state.nodes.clear();
    state.versions.clear();
    state.expanded.clear();
    state.filter = "";
    $("#search").value = "";
    state.lib = state.mounts.find((m) => m.id === id) || null;
    $("#libname").textContent = state.lib ? state.lib.name : "";
    document.title = `SyncScope — ${state.lib ? state.lib.name : ""}`;
    const [sel, st] = await Promise.all([
      api(`api/selection?mount=${q(state.active)}`),
      api(`api/status?mount=${q(state.active)}`),
    ]);
    state.inc = sel.included || [];
    state.exc = sel.excluded || [];
    state.status = st;
    await ensureLoaded("");
    await navigate("");
  }

  async function init() {
    document.documentElement.dataset.theme = state.theme;
    const params = new URLSearchParams(location.search);
    if (params.get("view") === "grid") state.view = "grid";
    if (params.get("view") === "list") state.view = "list";
    try {
      const data = await api("api/mounts");
      state.mounts = data.mounts || [];
      if (!state.mounts.length) throw new Error("нет доступных хранилищ");
      const wanted = params.get("mount");
      state.active = state.mounts.some((m) => m.id === wanted) ? wanted : state.mounts[0].id;
      state.lib = state.mounts.find((m) => m.id === state.active);
      const [sel, st] = await Promise.all([
        api(`api/selection?mount=${q(state.active)}`),
        api(`api/status?mount=${q(state.active)}`),
      ]);
      state.inc = sel.included || [];
      state.exc = sel.excluded || [];
      state.status = st;
      $("#libname").textContent = state.lib.name;
      document.title = `SyncScope — ${state.lib.name}`;
      await ensureLoaded("");
      const initial = params.get("path") || "";
      if (initial) {
        await ensureLoaded(initial);
        for (const a of ancestors(initial)) state.expanded.add(a);
        state.current = initial;
      }
      render();
      setInterval(poll, 3000);
    } catch (e) {
      $("#browser").innerHTML = `<div class="empty">Ошибка загрузки: ${String(e.message).replace(/[<>&]/g, "")}</div>`;
    }
  }

  $("#view-list").onclick = () => { state.view = "list"; localStorage.setItem("syncscope-view", state.view); renderBrowser(); renderHead(); };
  $("#view-grid").onclick = () => { state.view = "grid"; localStorage.setItem("syncscope-view", state.view); renderBrowser(); renderHead(); };
  $("#theme").onclick = () => {
    state.theme = state.theme === "dark" ? "light" : "dark";
    localStorage.setItem("syncscope-theme", state.theme);
    document.documentElement.dataset.theme = state.theme;
  };
  const refresh = async () => {
    await ensureLoaded(state.current, true);
    await ensureLoaded("", true);
    await refreshStatus();
    render();
  };
  $("#refresh").onclick = refresh;
  $("#refresh2").onclick = refresh;
  $("#search").oninput = (e) => {
    state.filter = e.target.value.trim();
    renderBrowser();
    renderHead();
  };
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closePreview();
    if (e.key === "/" && !["INPUT", "TEXTAREA"].includes(document.activeElement?.tagName)) {
      e.preventDefault();
      $("#search").focus();
    }
  });
  $("#preview").addEventListener("click", (e) => {
    if (e.target.closest("[data-close]")) closePreview();
  });
  init();
})();
