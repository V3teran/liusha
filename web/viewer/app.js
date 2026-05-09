// liusha graph viewer SPA — vanilla JS module，无 build pipeline。
// 依赖 d3@7 + dagre-d3@0.6.4（CDN 加载，见 index.html）。
//
// 启动流程（自动化）：
//   1. fetch /viewer/config.json → 若 dev 模式开（LIUSHA_VIEWER_DEV_KEY=1），
//      把 api_key 填到输入框；否则用 localStorage 旧值
//   2. fetch /engagement → 拉最近 engagement 列表，填进 <select> 下拉
//   3. 默选最新 engagement → fetch /graph/<eid> 渲染
//   4. 用户切下拉 / 改 host / 点"加载" / 勾"自动刷新" 都触发对应动作

const $ = (sel) => document.querySelector(sel);
const STORAGE_KEYS = {
  eid: 'liusha_viewer_eid',
  host: 'liusha_viewer_host',
  apikey: 'liusha_viewer_apikey',
};

const state = {
  view: null,
  selectedNodeID: null,
  autoTimer: null,
  engagements: [],
};

// ---------- 初始化 ----------

async function init() {
  // 恢复 localStorage 历史输入（host / apikey 缺省值）
  $('#input-host').value = localStorage.getItem(STORAGE_KEYS.host) || '';
  $('#input-apikey').value = localStorage.getItem(STORAGE_KEYS.apikey) || '';

  // dev 模式：拉 /viewer/config.json 自动填 api_key（覆盖 localStorage 旧值）
  await tryAutofillAPIKey();

  bindEvents();

  // 初次加载 engagement 列表，填下拉，默选最新，自动 render
  await loadEngagementList(/*autoLoadGraph=*/ true);
}

async function tryAutofillAPIKey() {
  try {
    // 路径在 /viewer/ 同级而非子路径——Gin StaticFS catch-all 不允许同前缀混搭具名路径。
    const res = await fetch('/viewer-config.json', { cache: 'no-store' });
    if (!res.ok) return; // dev 模式没开：404，正常
    const cfg = await res.json();
    if (cfg.api_key) {
      $('#input-apikey').value = cfg.api_key;
      localStorage.setItem(STORAGE_KEYS.apikey, cfg.api_key);
    }
  } catch (_) {
    // 网络错误也忽略，dev 便利失败回退到手动填
  }
}

function bindEvents() {
  $('#btn-load').addEventListener('click', () => {
    persistInputs();
    loadGraph();
  });

  $('#btn-refresh-list').addEventListener('click', () => {
    loadEngagementList(/*autoLoadGraph=*/ false);
  });

  $('#select-eid').addEventListener('change', (e) => {
    const eid = e.target.value;
    if (eid) {
      localStorage.setItem(STORAGE_KEYS.eid, eid);
      loadGraph();
    }
  });

  $('#chk-auto').addEventListener('change', (e) => {
    if (e.target.checked) {
      state.autoTimer = setInterval(loadGraph, 5000);
    } else {
      clearInterval(state.autoTimer);
      state.autoTimer = null;
    }
  });

  document.querySelectorAll('.tab').forEach((btn) => {
    btn.addEventListener('click', () => switchTab(btn.dataset.tab));
  });

  ['input-host', 'input-apikey'].forEach((id) => {
    $('#' + id).addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        persistInputs();
        loadEngagementList(/*autoLoadGraph=*/ true);
      }
    });
  });
}

function persistInputs() {
  localStorage.setItem(STORAGE_KEYS.host, $('#input-host').value.trim());
  localStorage.setItem(STORAGE_KEYS.apikey, $('#input-apikey').value);
}

// ---------- engagement 列表 ----------

async function loadEngagementList(autoLoadGraph) {
  const apikey = $('#input-apikey').value;
  const host = $('#input-host').value.trim();
  if (!apikey) {
    setStatus('error', 'NEED API KEY');
    return;
  }

  setStatus('loading', 'LIST...');
  const url = host ? `/engagement?host=${encodeURIComponent(host)}` : '/engagement';

  try {
    const res = await fetch(url, { headers: { 'X-API-Key': apikey } });
    if (!res.ok) {
      setStatus('error', `LIST HTTP ${res.status}`);
      return;
    }
    const data = await res.json();
    state.engagements = data.engagements || [];
    populateSelect();

    if (autoLoadGraph && state.engagements.length > 0) {
      // 优先恢复 localStorage 里的 eid（如果还在列表里）；否则选最新（第 0 个）
      const remembered = localStorage.getItem(STORAGE_KEYS.eid);
      const stillExists = state.engagements.find((e) => e.id === remembered);
      const target = stillExists ? remembered : state.engagements[0].id;
      $('#select-eid').value = target;
      localStorage.setItem(STORAGE_KEYS.eid, target);
      await loadGraph();
    } else {
      setStatus('idle', `LIST OK (${state.engagements.length})`);
    }
  } catch (err) {
    setStatus('error', 'LIST NETWORK ERROR');
    console.error(err);
  }
}

function populateSelect() {
  const sel = $('#select-eid');
  sel.innerHTML = '';
  if (state.engagements.length === 0) {
    sel.innerHTML = '<option value="">— 暂无 engagement —</option>';
    return;
  }
  sel.innerHTML = '<option value="">— 选择 engagement —</option>';
  for (const e of state.engagements) {
    const opt = document.createElement('option');
    opt.value = e.id;
    const time = e.created_at ? e.created_at.replace(/T/, ' ').replace(/\+.*$/, '') : '';
    opt.textContent =
      `${e.target_host} · ${e.status} · finding=${e.finding_count} · ${time} · ${e.id.slice(0, 8)}`;
    sel.appendChild(opt);
  }
}

// ---------- graph 加载 ----------

async function loadGraph() {
  const eid = $('#select-eid').value || localStorage.getItem(STORAGE_KEYS.eid);
  const host = $('#input-host').value.trim();
  const apikey = $('#input-apikey').value;

  if (!eid) {
    setStatus('error', 'PICK EID');
    return;
  }
  if (!apikey) {
    setStatus('error', 'NEED API KEY');
    return;
  }

  setStatus('loading', 'LOADING');
  $('#btn-load').disabled = true;

  const url = host
    ? `/graph/${encodeURIComponent(eid)}?host=${encodeURIComponent(host)}`
    : `/graph/${encodeURIComponent(eid)}`;

  try {
    const res = await fetch(url, { headers: { 'X-API-Key': apikey } });
    if (!res.ok) {
      const txt = await res.text();
      setStatus('error', `HTTP ${res.status}`);
      console.error('graph fetch failed:', txt);
      return;
    }
    const view = await res.json();
    state.view = view;
    renderAll();
    setStatus('active', 'ACTIVE');
  } catch (err) {
    setStatus('error', 'NETWORK ERROR');
    console.error(err);
  } finally {
    $('#btn-load').disabled = false;
  }
}

function setStatus(kind, label) {
  const el = $('#meta-status');
  el.className = `status status-${kind}`;
  el.textContent = label;
}

// ---------- 渲染 ----------

function renderAll() {
  const v = state.view;
  $('#meta-engagement').textContent = `${v.host || '-'} · ${v.engagement_id.slice(0, 8)}`;
  $('#meta-counts').textContent = `${v.nodes.length} nodes · ${v.edges.length} edges`;
  $('#empty-hint').style.display = v.nodes.length <= 2 ? 'flex' : 'none';

  renderGraph(v);
  renderFindings(v);
  $('#panel-raw').textContent = JSON.stringify(v, null, 2);
}

function renderGraph(view) {
  const g = new dagreD3.graphlib.Graph()
    .setGraph({
      rankdir: 'TB',
      nodesep: 40,
      ranksep: 60,
      marginx: 20,
      marginy: 20,
    })
    .setDefaultEdgeLabel(() => ({}));

  for (const n of view.nodes) {
    const sev = (n.payload && n.payload.severity) || '';
    const classes = ['kind-' + n.kind, sev ? 'severity-' + sev : ''].filter(Boolean).join(' ');
    const shape = n.kind === 'origin' || n.kind === 'goal' ? 'ellipse' : 'rect';
    g.setNode(n.id, {
      label: truncate(n.label || n.id, 40),
      class: classes,
      rx: 6,
      ry: 6,
      shape,
      paddingX: 12,
      paddingY: 8,
    });
  }

  for (const e of view.edges) {
    if (!g.hasNode(e.from) || !g.hasNode(e.to)) continue;
    g.setEdge(e.from, e.to, {
      label: e.label ? truncate(e.label, 24) : '',
      class: 'kind-' + e.kind,
      curve: d3.curveBasis,
      arrowheadStyle: 'fill: inherit; stroke: none;',
    });
  }

  const svg = d3.select('#graph');
  const inner = svg.select('g');
  inner.selectAll('*').remove();

  const zoom = d3.zoom().on('zoom', (e) => inner.attr('transform', e.transform));
  svg.call(zoom);

  const render = new dagreD3.render();
  render(inner, g);

  const bbox = inner.node().getBBox();
  const svgBBox = svg.node().getBoundingClientRect();
  const scale = Math.min(
    (svgBBox.width - 40) / Math.max(bbox.width, 1),
    (svgBBox.height - 40) / Math.max(bbox.height, 1),
    1.2,
  );
  const tx = (svgBBox.width - bbox.width * scale) / 2 - bbox.x * scale;
  const ty = 20 - bbox.y * scale;
  svg.call(zoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale));

  inner.selectAll('g.node').on('click', (event, id) => {
    state.selectedNodeID = id;
    inner.selectAll('g.node').classed('selected', false);
    d3.select(event.currentTarget).classed('selected', true);
    renderDetail(view.nodes.find((n) => n.id === id));
    switchTab('detail');
  });
}

function renderDetail(node) {
  const panel = $('#panel-detail');
  if (!node) {
    panel.className = 'empty';
    panel.textContent = '点击图中节点查看详情';
    return;
  }
  panel.className = '';
  const blocks = [];
  blocks.push(field('KIND', node.kind));
  blocks.push(field('LABEL', node.label));
  blocks.push(field('ID', node.id));
  if (node.payload) {
    if (node.payload.severity) {
      blocks.push(
        `<div class="detail-block"><div class="detail-label">SEVERITY</div>` +
          `<span class="severity-badge severity-${node.payload.severity}">${node.payload.severity}</span></div>`,
      );
    }
    blocks.push(field('PAYLOAD', JSON.stringify(node.payload, null, 2)));
  }
  panel.innerHTML = blocks.join('');
}

function field(label, value) {
  return `<div class="detail-block">
    <div class="detail-label">${label}</div>
    <div class="detail-value">${escapeHtml(String(value || '—'))}</div>
  </div>`;
}

function renderFindings(view) {
  const ul = $('#panel-findings');
  ul.innerHTML = '';
  const findings = view.nodes.filter((n) => n.kind === 'finding');
  if (findings.length === 0) {
    ul.innerHTML = '<li class="empty" style="text-align:center;color:var(--text-muted)">暂无 finding</li>';
    return;
  }
  for (const f of findings) {
    const sev = (f.payload && f.payload.severity) || 'info';
    const li = document.createElement('li');
    li.innerHTML = `
      <div class="finding-title">${escapeHtml(f.label)}</div>
      <div class="finding-meta">
        <span class="severity-badge severity-${sev}">${sev}</span>
        <span>${escapeHtml((f.payload && f.payload.kind) || '')}</span>
      </div>
    `;
    li.addEventListener('click', () => {
      state.selectedNodeID = f.id;
      renderDetail(f);
      switchTab('detail');
      d3.select('#graph').select('g').selectAll('g.node').classed('selected', false);
      d3.select('#graph').select('g')
        .selectAll('g.node')
        .filter((id) => id === f.id)
        .classed('selected', true);
    });
    ul.appendChild(li);
  }
}

function switchTab(tab) {
  document.querySelectorAll('.tab').forEach((b) => b.classList.toggle('active', b.dataset.tab === tab));
  document.querySelectorAll('.tab-panel').forEach((p) => p.classList.toggle('hidden', p.dataset.panel !== tab));
}

// ---------- helpers ----------

function truncate(s, n) {
  if (!s) return '';
  return s.length > n ? s.slice(0, n - 1) + '…' : s;
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

init();
