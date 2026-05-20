// liusha graph viewer SPA — vanilla JS module，无 build pipeline。
// 依赖 d3@7 + dagre-d3@0.6.4（CDN 加载，见 index.html）。
//
// 启动流程（自动化）：
//   1. fetch /viewer/config.json → 若 dev 模式开（LIUSHA_VIEWER_DEV_KEY=1），
//      把 api_key 填到输入框；否则用 localStorage 旧值
//   2. fetch /session → 拉最近 session 列表，填进 <select> 下拉
//   3. 默选最新 session → fetch /graph/<eid> 渲染
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
  sessions: [],
};

// ---------- 初始化 ----------

async function init() {
  // 恢复 localStorage 历史输入（host / apikey 缺省值）
  $('#input-host').value = localStorage.getItem(STORAGE_KEYS.host) || '';
  $('#input-apikey').value = localStorage.getItem(STORAGE_KEYS.apikey) || '';

  // dev 模式：拉 /viewer/config.json 自动填 api_key（覆盖 localStorage 旧值）
  await tryAutofillAPIKey();

  bindEvents();

  // 初次加载 session 列表，填下拉，默选最新，自动 render
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

  // View 切换：Graph（图视图）/ LLM（调用审计全屏）
  $('#btn-view-graph').addEventListener('click', () => switchView('graph'));
  $('#btn-view-llm').addEventListener('click', () => switchView('llm'));
  $('#btn-view-tasks').addEventListener('click', () => switchView('tasks'));

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

// ---------- session 列表 ----------

async function loadEngagementList(autoLoadGraph) {
  const apikey = $('#input-apikey').value;
  const host = $('#input-host').value.trim();
  if (!apikey) {
    setStatus('error', 'NEED API KEY');
    return;
  }

  setStatus('loading', 'LIST...');
  const url = host ? `/session?host=${encodeURIComponent(host)}` : '/session';

  try {
    const res = await fetch(url, { headers: { 'X-API-Key': apikey } });
    if (!res.ok) {
      setStatus('error', `LIST HTTP ${res.status}`);
      return;
    }
    const data = await res.json();
    state.sessions = data.sessions || [];
    populateSelect();

    if (autoLoadGraph && state.sessions.length > 0) {
      // 优先恢复 localStorage 里的 eid（如果还在列表里）；否则选最新（第 0 个）
      const remembered = localStorage.getItem(STORAGE_KEYS.eid);
      const stillExists = state.sessions.find((e) => e.id === remembered);
      const target = stillExists ? remembered : state.sessions[0].id;
      $('#select-eid').value = target;
      localStorage.setItem(STORAGE_KEYS.eid, target);
      await loadGraph();
    } else {
      setStatus('idle', `LIST OK (${state.sessions.length})`);
    }
  } catch (err) {
    setStatus('error', 'LIST NETWORK ERROR');
    console.error(err);
  }
}

function populateSelect() {
  const sel = $('#select-eid');
  sel.innerHTML = '';
  if (state.sessions.length === 0) {
    sel.innerHTML = '<option value="">— 暂无 session —</option>';
    return;
  }
  sel.innerHTML = '<option value="">— 选择 session —</option>';
  for (const e of state.sessions) {
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
    if (res.status === 404) {
      // session 已不在 DB（被 truncate / 已归档）→ 清 stale localStorage、
      // 重拉列表自动选最新；不要让用户手动改下拉。
      console.warn(`graph 404: session ${eid} 已不存在，清 stale eid 并重拉列表`);
      localStorage.removeItem(STORAGE_KEYS.eid);
      setStatus('error', 'STALE EID, RELOADING');
      $('#btn-load').disabled = false;
      await loadEngagementList(/*autoLoadGraph=*/ true);
      return;
    }
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
    // 异步拉 LLM invocation 审计——与 graph 同 session，失败不阻塞主流程。
    loadInvocations(eid, apikey).catch((err) => console.warn('invocations load failed:', err));
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
  // 防御性 null-guard：后端在某些路径（空 session / 投影器异常）可能返
  // nodes/edges 字段为 null 而非 []，直接 .length 会抛 TypeError。
  if (!v) {
    setStatus('error', 'NO VIEW');
    return;
  }
  const nodes = v.nodes || [];
  const edges = v.edges || [];
  const eidShort = v.owner_id ? v.owner_id.slice(0, 8) : '-';
  $('#meta-session').textContent = `${v.host || '-'} · ${eidShort}`;
  $('#meta-counts').textContent = `${nodes.length} nodes · ${edges.length} edges`;
  $('#empty-hint').style.display = nodes.length <= 2 ? 'flex' : 'none';

  renderGraph({ ...v, nodes, edges });
  renderFindings({ ...v, nodes, edges });
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

/**
 * 切换主视图：Graph（攻击图）/ LLM（调用审计全屏）。
 * @param {'graph'|'llm'} view
 */
function switchView(view) {
  const graphView = $('#view-graph');
  const llmView = $('#view-llm');
  const tasksView = $('#view-tasks');
  const btnGraph = $('#btn-view-graph');
  const btnLlm = $('#btn-view-llm');
  const btnTasks = $('#btn-view-tasks');

  // 全部先重置
  graphView.classList.add('hidden');
  llmView.classList.add('hidden');
  tasksView.classList.add('hidden');
  btnGraph.classList.remove('active');
  btnLlm.classList.remove('active');
  btnTasks.classList.remove('active');

  const eid = $('#select-eid').value || localStorage.getItem(STORAGE_KEYS.eid);
  const apikey = $('#input-apikey').value;

  if (view === 'llm') {
    llmView.classList.remove('hidden');
    btnLlm.classList.add('active');
    if (eid && apikey) {
      loadInvocations(eid, apikey).catch((err) => console.warn('invocations load failed:', err));
    }
  } else if (view === 'tasks') {
    tasksView.classList.remove('hidden');
    btnTasks.classList.add('active');
    if (eid && apikey) {
      loadAgentRuns(eid, apikey).catch((err) => console.warn('agent_runs load failed:', err));
    }
  } else {
    graphView.classList.remove('hidden');
    btnGraph.classList.add('active');
  }
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

// ---------- LLM invocation 审计（按 agent_run_id 分组）----------

/**
 * fetch /llm/invocations/:eid → 渲染到 #panel-invocations。
 * 按 agent_run_id 分组折叠（<details>），点开展示完整 16 字段（含 messages / result jsonb）。
 * @param {string} eid owner_id
 * @param {string} apikey X-API-Key
 */
async function loadInvocations(eid, apikey) {
  const panel = $('#panel-invocations');
  if (!panel) return;
  panel.innerHTML = '<div class="empty">LOADING...</div>';
  const res = await fetch(`/llm/invocations/${encodeURIComponent(eid)}`, {
    headers: { 'X-API-Key': apikey },
  });
  if (!res.ok) {
    panel.innerHTML = `<div class="empty">HTTP ${res.status}</div>`;
    return;
  }
  const data = await res.json();
  renderInvocations(data);
}

/**
 * 渲染 invocation 分组到 #panel-invocations。
 * @param {{owner_id: string, total: number, groups: Array<{agent_run_id: string, count: number, invocations: Array}>}} data
 */
function renderInvocations(data) {
  const panel = $('#panel-invocations');
  if (!data || !Array.isArray(data.groups) || data.groups.length === 0) {
    panel.innerHTML = '<div class="empty">该 session 暂无 LLM invocation</div>';
    return;
  }

  const totalCost = data.groups.reduce((acc, g) => {
    return acc + g.invocations.reduce((s, inv) => s + (inv.cost_usd || 0), 0);
  }, 0);

  const summary = `
    <div class="inv-header">
      <span><strong>${data.total}</strong> invocations</span>
      <span>${data.groups.length} agent_run 分组</span>
      <span>总成本 $${totalCost.toFixed(4)}</span>
    </div>
  `;

  const groupsHtml = data.groups
    .map((g) => renderInvocationGroup(g))
    .join('');

  panel.innerHTML = summary + groupsHtml;
}

function renderInvocationGroup(group) {
  const arid = group.agent_run_id || 'unassigned';
  const aridShort = arid === 'unassigned' ? arid : arid.slice(0, 8);
  const inTokens = group.invocations.reduce((s, i) => s + (i.in_tokens || 0), 0);
  const outTokens = group.invocations.reduce((s, i) => s + (i.out_tokens || 0), 0);
  const cost = group.invocations.reduce((s, i) => s + (i.cost_usd || 0), 0);

  const rows = group.invocations.map((inv, idx) => renderInvocationCard(inv, idx + 1)).join('');

  return `
    <details class="inv-group" open>
      <summary>
        <strong>agent_run ${escapeHtml(aridShort)}</strong>
        · ${group.count} 步
        · tok in=${inTokens} out=${outTokens}
        · $${cost.toFixed(4)}
      </summary>
      <div class="inv-list">${rows}</div>
    </details>
  `;
}

function renderInvocationCard(inv, stepIdx) {
  const time = inv.created_at ? inv.created_at.replace(/T/, ' ').replace(/\.\d+/, '').replace(/\+.*$/, '') : '';
  const errBadge = inv.error_message
    ? `<span class="inv-err">ERROR: ${escapeHtml(truncate(inv.error_message, 80))}</span>`
    : '';

  return `
    <details class="inv-card">
      <summary>
        <span class="inv-step">#${stepIdx}</span>
        <span class="inv-purpose">${escapeHtml(inv.call_purpose || '-')}</span>
        <span class="inv-model">${escapeHtml(inv.provider)}/${escapeHtml(inv.model)}</span>
        <span class="inv-tokens">in=${inv.in_tokens} out=${inv.out_tokens}${inv.cached_tokens ? ' cached=' + inv.cached_tokens : ''}</span>
        <span class="inv-cost">$${(inv.cost_usd || 0).toFixed(6)}</span>
        <span class="inv-lat">${inv.latency_ms}ms</span>
        <span class="inv-finish">${escapeHtml(inv.finish_reason || '-')}</span>
        <span class="inv-time">${time}</span>
        ${errBadge}
      </summary>
      <dl class="inv-fields">
        <dt>id</dt><dd>${inv.id}</dd>
        <dt>agent_run_id</dt><dd>${escapeHtml(inv.agent_run_id || '(null)')}</dd>
        <dt>owner_id</dt><dd>${escapeHtml(inv.owner_id || '(null)')}</dd>
        <dt>provider</dt><dd>${escapeHtml(inv.provider)}</dd>
        <dt>model</dt><dd>${escapeHtml(inv.model)}</dd>
        <dt>call_purpose</dt><dd>${escapeHtml(inv.call_purpose || '')}</dd>
        <dt>in_tokens</dt><dd>${inv.in_tokens}</dd>
        <dt>out_tokens</dt><dd>${inv.out_tokens}</dd>
        <dt>cached_tokens</dt><dd>${inv.cached_tokens}</dd>
        <dt>cost_usd</dt><dd>$${inv.cost_usd}</dd>
        <dt>latency_ms</dt><dd>${inv.latency_ms} ms</dd>
        <dt>finish_reason</dt><dd>${escapeHtml(inv.finish_reason || '')}</dd>
        <dt>error_message</dt><dd>${escapeHtml(inv.error_message || '')}</dd>
        <dt>created_at</dt><dd>${escapeHtml(inv.created_at || '')}</dd>
        <dt>messages (jsonb)</dt><dd><pre class="inv-json">${escapeHtml(JSON.stringify(inv.messages, null, 2))}</pre></dd>
        <dt>result (jsonb)</dt><dd><pre class="inv-json">${escapeHtml(JSON.stringify(inv.result, null, 2))}</pre></dd>
      </dl>
    </details>
  `;
}

// ---------- Agent run 父子树（subtask swarm 可观测）----------

/**
 * fetch /agent_runs/:eid → 按 parent_id 拼树 → 渲染嵌套 ul。
 * 根节点 = parent_id 为空的 agent_run（独立 task 或父 active）。
 * @param {string} eid owner_id
 * @param {string} apikey X-API-Key
 */
async function loadAgentRuns(eid, apikey) {
  const panel = $('#panel-tasks');
  if (!panel) return;
  panel.innerHTML = '<div class="empty">LOADING...</div>';
  const res = await fetch(`/agent_runs/${encodeURIComponent(eid)}`, {
    headers: { 'X-API-Key': apikey },
  });
  if (!res.ok) {
    panel.innerHTML = `<div class="empty">load failed: ${res.status}</div>`;
    return;
  }
  const data = await res.json();
  const runs = data.runs || [];
  if (runs.length === 0) {
    panel.innerHTML = `<div class="empty">无 agent_run 记录</div>`;
    return;
  }

  // 按 parent_id 拼树
  const byId = {};
  const roots = [];
  for (const r of runs) {
    byId[r.id] = { ...r, children: [] };
  }
  for (const r of runs) {
    if (r.parent_id && byId[r.parent_id]) {
      byId[r.parent_id].children.push(byId[r.id]);
    } else {
      roots.push(byId[r.id]);
    }
  }

  panel.innerHTML = `
    <div class="tasks-header">total=${data.total} · roots=${roots.length}</div>
    <ul class="task-tree">${roots.map(renderTaskNode).join('')}</ul>
  `;
}

/**
 * 递归渲染单个 agent_run 节点 + 嵌套子树。
 * @param {object} node 含 children 字段的 agent_run
 * @returns {string} HTML
 */
function renderTaskNode(node) {
  const short = node.id.slice(0, 8);
  const status = node.status;
  const result = node.result || {};
  const input = node.input || {};
  const meta = [];
  if (result.total_steps) meta.push(`steps=${result.total_steps}`);
  if (result.terminate_by) meta.push(`by=${result.terminate_by}`);
  if (result.total_in) meta.push(`in=${result.total_in}`);
  if (result.total_out) meta.push(`out=${result.total_out}`);

  let briefSnippet = '';
  const ep = input.entrypoint;
  if (ep) {
    if (typeof ep === 'object') {
      if (ep.brief) briefSnippet = ` · ${ep.brief.slice(0, 80)}`;
      else if (ep.url) briefSnippet = ` · ${ep.method || ''} ${String(ep.url).slice(0, 70)}`;
    }
  }

  const childrenHtml = node.children.length
    ? `<ul class="task-children">${node.children.map(renderTaskNode).join('')}</ul>`
    : '';

  return `<li class="task-node status-${escapeHtml(status)}">
    <code class="task-id">${escapeHtml(short)}</code>
    <span class="task-role">[${escapeHtml(node.role)}]</span>
    <span class="task-status task-status-${escapeHtml(status)}">${escapeHtml(status)}</span>
    ${meta.length ? `<span class="task-meta">${escapeHtml(meta.join(' · '))}</span>` : ''}
    <span class="task-brief">${escapeHtml(briefSnippet)}</span>
    ${childrenHtml}
  </li>`;
}

init();
