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
    busy: false,
  };

  const $ = (sel) => document.querySelector(sel);
  const isAncestor = (a, b) => b.startsWith(a + "/");
  const covered = (p, roots) => roots.some((r) => p === r || isAncestor(r, p));
  const hasDesc = (p, roots) => roots.some((r) => isAncestor(p, r));

  function selected(p) {
    return covered(p, state.inc) && !covered(p, state.exc);
  }

  function partial(p) {
    if (covered(p, state.exc)) return false;
    return !selected(p) && hasDesc(p, state.inc);
  }

  async function api(path, opts = {}) {
    const res = await fetch(path, {
      headers: { "Content-Type": "application/json" },
      ...opts,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
    return data;
  }

  function fmtSize(n) {
    if (n === undefined || n === null) return "—";
    if (n < 1024) return `${n} Б`;
    const units = ["КБ", "МБ", "ГБ", "ТБ"];
    let v = n / 1024;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return `${v.toFixed(v >= 10 ? 0 : 1)} ${units[i]}`;
  }

  function fmtDate(s) {
    if (!s) return "—";
    const d = new Date(s);
    if (Number.isNaN(d.getTime())) return "—";
    return d.toLocaleString("ru-RU", {
      day: "2-digit", month: "2-digit", year: "2-digit",
      hour: "2-digit", minute: "2-digit",
    });
  }

  async function ensureLoaded(path, force = false) {
    if (!force && state.nodes.has(path)) return;
    const page = await api(`/api/catalog?parent=${encodeURIComponent(path)}`);
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
    if (path) {
      const parent = path.split("/").slice(0, -1).join("/");
      await ensureLoaded(parent, force);
    }
    render();
  }

  function statusFor(node) {
    const p = node.path;
    if (!selected(p)) {
      if (covered(p, state.exc)) return { text: "исключено", cls: "" };
      return { text: "на сервере", cls: "" };
    }
    if (!node.is_dir && partial(p)) return { text: "частично", cls: "info" };
    const st = state.status || {};
    if (st.errors > 0) return { text: "ошибка", cls: "bad" };
    if (st.state && st.state !== "idle") return { text: "синхронизация", cls: "warn" };
    if (st.need_files > 0) return { text: "в очереди", cls: "warn" };
    return { text: "синхронизировано", cls: "ok" };
  }

  function checkboxFor(node) {
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.disabled = !node.is_dir;
    if (node.is_dir) {
      const sel = selected(node.path);
      cb.checked = sel;
      cb.indeterminate = !sel && partial(node.path);
      cb.addEventListener("change", (e) => {
        e.stopPropagation();
        toggleDir(node.path, cb.checked);
      });
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
      const exact = inc.includes(path);
      inc = inc.filter((x) => !(x === path || isAncestor(path, x)));
      exc = exc.filter((x) => !(x === path || isAncestor(path, x)));
      if (covered(path, inc)) exc.push(path);
    }
    await saveSelection(inc, exc);
  }

  async function saveSelection(inc, exc) {
    state.busy = true;
    try {
      const data = await api("/api/selection", {
        method: "POST",
        body: JSON.stringify({ included: inc, excluded: exc }),
      });
      state.inc = data.selection.included || [];
      state.exc = data.selection.excluded || [];
      render();
      await refreshStatus();
    } catch (e) {
      alert("Не удалось применить выбор: " + e.message);
    } finally {
      state.busy = false;
    }
  }

  async function freeSpace(path) {
    if (!confirm(`Удалить локальную копию «${path}»? Файлы останутся на сервере.`)) return;
    try {
      await api("/api/free-space", {
        method: "POST",
        body: JSON.stringify({ path }),
      });
      const sel = await api("/api/selection");
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
    const parts = state.current.split("/").filter(Boolean);
    const root = document.createElement("button");
    root.textContent = "Корень";
    root.onclick = () => navigate("");
    el.appendChild(root);
    let acc = "";
    for (const part of parts) {
      acc = acc ? `${acc}/${part}` : part;
      const sep = document.createElement("span");
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
    const rootNodes = state.nodes.get("") || [];
    if (!rootNodes.length) {
      const d = document.createElement("div");
      d.className = "muted";
      d.textContent = state.nodes.has("") ? "Пусто" : "Загрузка…";
      root.appendChild(d);
      return;
    }
    const ul = document.createElement("ul");
    const renderLevel = (list, container, depth) => {
      for (const node of list) {
        if (!node.is_dir) continue;
        const li = document.createElement("li");
        const row = document.createElement("div");
        row.className = "tree-row" + (state.current === node.path ? " active" : "");
        row.style.paddingLeft = `${depth * 12 + 4}px`;

        const chev = document.createElement("span");
        chev.className = "chev";
        chev.textContent = state.expanded.has(node.path) ? "▾" : "▸";
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

        const cb = checkboxFor(node);
        cb.addEventListener("click", (e) => e.stopPropagation());
        row.append(chev, label, cb);
        li.appendChild(row);

        if (state.expanded.has(node.path)) {
          const kids = state.nodes.get(node.path);
          if (kids) {
            const sub = document.createElement("ul");
            renderLevel(kids, sub, depth + 1);
            li.appendChild(sub);
          } else {
            ensureLoaded(node.path).then(renderTree).catch(() => {});
          }
        }
        container.appendChild(li);
      }
    };
    renderLevel(rootNodes, ul, 0);
    root.appendChild(ul);
  }

  function renderRows() {
    const body = $("#rows");
    body.textContent = "";
    const nodes = (state.nodes.get(state.current) || []).filter((n) => {
      if (!state.filter) return true;
      return n.name.toLowerCase().includes(state.filter.toLowerCase());
    });
    if (!nodes.length) {
      const tr = document.createElement("tr");
      const td = document.createElement("td");
      td.colSpan = 6;
      td.className = "muted";
      td.textContent = state.nodes.has(state.current) ? "Нет файлов" : "Загрузка…";
      tr.appendChild(td);
      body.appendChild(tr);
      return;
    }
    const tpl = $("#row-template");
    for (const node of nodes) {
      const tr = tpl.content.firstElementChild.cloneNode(true);
      tr.dataset.path = node.path;
      tr.querySelector(".check").appendChild(checkboxFor(node));

      const name = tr.querySelector(".name .link");
      name.textContent = (node.is_dir ? "▸ " : "· ") + node.name;
      if (node.is_dir) name.onclick = () => navigate(node.path);

      const sub = tr.querySelector(".sub");
      if (node.is_dir && state.expanded.has(node.path)) {
        sub.textContent = "открыта";
      }

      tr.querySelector(".size").textContent = node.is_dir ? "—" : fmtSize(node.size);
      tr.querySelector(".mtime").textContent = fmtDate(node.mod_time);
      const st = statusFor(node);
      const badge = tr.querySelector(".badge");
      badge.textContent = st.text;
      badge.className = "badge " + st.cls;

      const actions = tr.querySelector(".actions");
      if (node.is_dir && selected(node.path)) {
        const b = document.createElement("button");
        b.className = "mini";
        b.textContent = "Освободить";
        b.title = "Удалить локальную копию, оставить на сервере";
        b.onclick = () => freeSpace(node.path);
        actions.appendChild(b);
      }
      body.appendChild(tr);
    }
  }

  function renderStatus() {
    const st = state.status || {};
    const connected = !!st.connected;
    const relay = (st.connection_type || "").startsWith("relay");
    const conn = $("#conn");
    conn.textContent = connected ? (relay ? "relay" : "direct") : "офлайн";
    conn.className = "pill " + (connected ? (relay ? "warn" : "ok") : "bad");

    const sync = $("#sync");
    if (!connected) {
      sync.textContent = "нет связи";
      sync.className = "pill bad";
    } else if (st.errors > 0) {
      sync.textContent = "ошибка";
      sync.className = "pill bad";
    } else if (st.state !== "idle" || st.need_files > 0) {
      sync.textContent = "синхронизация";
      sync.className = "pill warn";
    } else {
      sync.textContent = "idle";
      sync.className = "pill ok";
    }

    const count = state.inc.length;
    $("#summary").textContent = count ? `Выбрано папок: ${count}` : "Ничего не выбрано";
    $("#statusbar").textContent = [
      `state: ${st.state || "—"}`,
      `need: ${st.need_files ?? "—"}`,
      `local: ${st.local_files ?? "—"}`,
      st.connection_address ? `peer: ${st.connection_address}` : "",
      state.lib ? `path: ${state.lib.local}` : "",
    ].filter(Boolean).join(" · ");
  }

  function render() {
    renderCrumbs();
    renderTree();
    renderRows();
    renderStatus();
  }

  async function refreshStatus() {
    try {
      state.status = await api("/api/status");
      renderStatus();
    } catch (_) {
      state.status = { connected: false, state: "unknown" };
      renderStatus();
    }
  }

  async function poll() {
    if (document.hidden || state.busy) return;
    await refreshStatus();
    try {
      const page = await api(`/api/catalog?parent=${encodeURIComponent(state.current)}`);
      if ((page.version || 0) !== (state.versions.get(state.current) || 0)) {
        state.nodes.set(state.current, page.nodes || []);
        state.versions.set(state.current, page.version || 0);
        render();
      }
    } catch (_) {}
  }

  async function init() {
    try {
      const [lib, sel, st] = await Promise.all([
        api("/api/library"),
        api("/api/selection"),
        api("/api/status"),
      ]);
      state.lib = lib;
      state.inc = sel.included || [];
      state.exc = sel.excluded || [];
      state.status = st;
      $("#libname").textContent = lib.name;
      document.title = `SyncScope — ${lib.name}`;
      await ensureLoaded("");
      render();
      setInterval(poll, 3000);
    } catch (e) {
      $("#rows").textContent = "";
      const td = document.createElement("td");
      td.colSpan = 6;
      td.className = "muted";
      td.textContent = "Ошибка загрузки: " + e.message;
      const tr = document.createElement("tr");
      tr.appendChild(td);
      $("#rows").appendChild(tr);
    }
  }

  $("#refresh").onclick = async () => {
    await ensureLoaded(state.current, true);
    await ensureLoaded("", true);
    await refreshStatus();
    render();
  };
  $("#search").oninput = (e) => {
    state.filter = e.target.value;
    renderRows();
  };
  init();
})();
