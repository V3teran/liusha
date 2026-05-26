// liusha sitemap viewer SPA — vanilla JS module，无 build pipeline，无外部依赖。
//
// 启动流程：
//   1. fetch /viewer-config.json → 若 dev 模式开（LIUSHA_VIEWER_DEV_KEY=1），
//      把 api_key 填到输入框；否则用 localStorage 旧值
//   2. fetch /session → 拉最近 session 列表，填进 <select> 下拉
//   3. 默选最新 session → fetch /sitemap/<oid> 渲染树
//   4. 用户切下拉 / 改 host / 点"加载" / 勾"自动刷新" 都触发对应动作

const $ = (sel) => document.querySelector(sel);
const STORAGE_KEYS = {
  eid: 'liusha_viewer_eid',
  host: 'liusha_viewer_host',
  apikey: 'liusha_viewer_apikey',
};

const state = {
  view: null,
  autoTimer: null,
  sessions: [],
};

// ---------- 初始化 ----------

async function init() {
  $('#input-host').value = localStorage.getItem(STORAGE_KEYS.host) || '';
  $('#input-apikey').value = localStorage.getItem(STORAGE_KEYS.apikey) || '';
  await tryAutofillAPIKey();
  bindEvents();
  await loadEngagementList(true);
}

async function tryAutofillAPIKey() {
  try {
    const res = await fetch('/viewer-config.json', { cache: 'no-store' });
    if (!res.ok) return;
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
    loadSitemap();
  });
  $('#btn-refresh-list').addEventListener('click', () => loadEngagementList(false));
  $('#select-eid').addEventListener('change', (e) => {
    const eid = e.target.value;
    if (eid) {
      localStorage.setItem(STORAGE_KEYS.eid, eid);
      loadSitemap();
    }
  });
  $('#chk-auto').addEventListener('change', (e) => {
    if (e.target.checked) state.autoTimer = setInterval(loadSitemap, 5000);
    else { clearInterval(state.autoTimer); state.autoTimer = null; }
  });
  document.querySelectorAll('.tab').forEach((btn) => {
    btn.addEventListener('click', () => switchTab(btn.dataset.tab));
  });
  $('#btn-view-sitemap').addEventListener('click', () => switchView('sitemap'));
  $('#btn-view-llm').addEventListener('click', () => switchView('llm'));
  $('#btn-view-tasks').addEventListener('click', () => switchView('tasks'));
  ['input-host', 'input-apikey'].forEach((id) => {
    $('#' + id).addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        persistInputs();
        loadEngagementList(true);
      }
    });
  });
}

function persistInputs() {
  localStorage.setItem(STORAGE_KEYS.host, $('#input-host').value.trim());
  localStorage.setItem(STORAGE_KEYS.apikey, $('#input-apikey').value);
}

// ---------- session 列表 ----------

async function loadEngagementList(autoLoad) {
  const apikey = $('#input-apikey').value;
  const host = $('#input-host').value.trim();
  if (!apikey) { setStatus('error', 'NEED API KEY'); return; }
  setStatus('loading', 'LIST...');
  const url = host ? `/session?host=${encodeURIComponent(host)}` : '/session';
  try {
    const res = await fetch(url, { headers: { 'X-API-Key': apikey } });
    if (!res.ok) { setStatus('error', `LIST HTTP ${res.status}`); return; }
    const data = await res.json();
    state.sessions = data.sessions || [];
    populateSelect();
    if (autoLoad && state.sessions.length > 0) {
      const remembered = localStorage.getItem(STORAGE_KEYS.eid);
      const stillExists = state.sessions.find((e) => e.id === remembered);
      const target = stillExists ? remembered : state.sessions[0].id;
      $('#select-eid').value = target;
      localStorage.setItem(STORAGE_KEYS.eid, target);
      await loadSitemap();
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
    opt.textContent = `${formatSessionLabel(e)} · ${e.status} · ${time} · ${e.id.slice(0, 8)}`;
    sel.appendChild(opt);
  }
}

// 从 scope JSON 提取人类可读标签——passive 模式取 host，active 取 brief 摘要
// 后端 OwnerSummary 不返 target_host/finding_count，统一在前端 fallback 解析
function formatSessionLabel(e) {
  const mode = e.mode || '?';
  let target = '';
  try {
    const scope = e.scope ? JSON.parse(e.scope) : {};
    if (scope.host) target = scope.host;
    else if (scope.brief) target = scope.brief.replace(/\s+/g, ' ').slice(0, 40);
  } catch (_) { /* malformed scope JSON 容错 */ }
  return target ? `${mode} · ${target}` : mode;
}

// ---------- sitemap 加载 ----------

async function loadSitemap() {
  const eid = $('#select-eid').value || localStorage.getItem(STORAGE_KEYS.eid);
  const host = $('#input-host').value.trim();
  const apikey = $('#input-apikey').value;
  if (!eid) { setStatus('error', 'PICK EID'); return; }
  if (!apikey) { setStatus('error', 'NEED API KEY'); return; }

  setStatus('loading', 'LOADING');
  $('#btn-load').disabled = true;

  const url = host
    ? `/sitemap/${encodeURIComponent(eid)}?host=${encodeURIComponent(host)}`
    : `/sitemap/${encodeURIComponent(eid)}`;

  try {
    const res = await fetch(url, { headers: { 'X-API-Key': apikey } });
    if (res.status === 404) {
      const body = await res.json().catch(() => ({}));
      const msg = body.error || 'NOT FOUND';
      // passive 模式 → 404 + 特定 error message
      if (msg.includes('仅支持 active 模式')) {
        setStatus('error', 'PASSIVE NO SITEMAP');
        $('#sitemap').innerHTML = '<div class="empty">passive 模式无 sitemap 视图——使用 LLM / Tasks Tab 或后端 /findings API</div>';
        $('#empty-hint').style.display = 'none';
        $('#btn-load').disabled = false;
        return;
      }
      console.warn(`sitemap 404: ${msg}`);
      localStorage.removeItem(STORAGE_KEYS.eid);
      setStatus('error', 'STALE EID, RELOADING');
      $('#btn-load').disabled = false;
      await loadEngagementList(true);
      return;
    }
    if (!res.ok) {
      const txt = await res.text();
      setStatus('error', `HTTP ${res.status}`);
      console.error('sitemap fetch failed:', txt);
      return;
    }
    const view = await res.json();
    state.view = view;
    renderAll();
    setStatus('active', 'ACTIVE');
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

// ---------- sitemap 渲染 ----------

function renderAll() {
  const v = state.view;
  if (!v) { setStatus('error', 'NO VIEW'); return; }
  const root = v.root;
  const eidShort = v.owner_id ? v.owner_id.slice(0, 8) : '-';
  $('#meta-session').textContent = `${v.host || '(all hosts)'} · ${eidShort}`;

  // 递归统计 endpoint + finding 数
  const counts = { endpoints: 0, findings: 0 };
  walkTree(root, (node) => {
    if (node.kind === 'endpoint') {
      counts.endpoints++;
      if (node.findings) counts.findings += node.findings.length;
    }
  });
  $('#meta-counts').textContent = `${counts.endpoints} endpoints · ${counts.findings} findings`;
  $('#empty-hint').style.display = counts.endpoints === 0 ? 'flex' : 'none';

  renderSitemap(v);
  renderFindings(root);
  $('#panel-raw').textContent = JSON.stringify(v, null, 2);
}

/**
 * 渲染 sitemap 为 D3 force-directed 辐射图。
 * domain 节点居中固定 → 第 1 圈 endpoint → 第 2 圈 finding。
 * 组合漏洞（chains a→c + b→c）虚线弧连接。
 *
 * 数据流：
 *   nested tree (root) + chains[]
 *     → walkTree 展开 flat nodes
 *     → 父子关系派生 contains links
 *     → chains 派生 enables links
 *     → D3 force simulation 物理布局 + drag + zoom
 *
 * @param {object} view {root, chains?}
 */
function renderSitemap(view) {
  const svg = d3.select('#sitemap');
  svg.select('g').selectAll('*').remove();

  const root = view.root;
  if (!root) return;

  // 展开树为 flat nodes，记录父子关系
  const nodes = [];
  const containsLinks = [];
  const nodeById = new Map();
  let nodeId = 0;
  const assignId = (node, parentId) => {
    const id = node.id || (node.kind === 'endpoint' && node.findings && node.findings[0] ? `endpoint:${node.path}:${node.method}` : `n${nodeId++}`);
    const n = {
      id,
      kind: node.kind,
      name: node.name,
      path: node.path,
      method: node.method,
      hasVuln: (node.findings || []).length > 0,
      findings: node.findings || [],
    };
    nodes.push(n);
    nodeById.set(id, n);
    if (parentId) containsLinks.push({ source: parentId, target: id, kind: 'contains' });

    // endpoint 下挂 finding 节点
    if (node.kind === 'endpoint' && node.findings) {
      for (const f of node.findings) {
        const fn = {
          id: `finding:${f.id}`,
          kind: 'finding',
          name: f.summary || '(no summary)',
          severity: (f.severity || 'info').toLowerCase(),
          cwe_id: f.cwe_id,
          finding_id: f.id,
        };
        nodes.push(fn);
        nodeById.set(fn.id, fn);
        containsLinks.push({ source: id, target: fn.id, kind: 'contains' });
      }
    }

    for (const c of (node.children || [])) assignId(c, id);
  };
  assignId(root, null);

  // chains: finding-a → finding-c 派生 enables links
  const chains = view.chains || [];
  const enablesLinks = chains.map((ch) => ({
    source: `finding:${ch.from}`,
    target: `finding:${ch.to}`,
    kind: 'enables',
  })).filter((l) => nodeById.has(l.source) && nodeById.has(l.target));

  const links = [...containsLinks, ...enablesLinks];

  // SVG 容器尺寸
  const svgEl = document.getElementById('sitemap');
  const width = svgEl.clientWidth || 1200;
  const height = svgEl.clientHeight || 800;

  // domain 固定中心（层级辐射的锚点）
  const domainNode = nodes.find((n) => n.kind === 'domain');
  if (domainNode) {
    domainNode.fx = width / 2;
    domainNode.fy = height / 2;
  }

  // D3 force simulation：层级辐射（forceRadial 按 kind 分层）+ 链接 + 排斥 + 碰撞
  // 业界对 force layout 的最佳实践：用 forceRadial 强制层级辐射，避免节点位置随机
  // domain（中心 r=0）→ endpoint（r=160）→ finding（r=300）
  const simulation = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).id((d) => d.id).distance((d) => d.kind === 'enables' ? 150 : 50).strength(0.3))
    .force('charge', d3.forceManyBody().strength(-180))
    .force('collision', d3.forceCollide().radius((d) => nodeRadius(d) + 6))
    .force('radial', d3.forceRadial((d) => layerRadius(d), width / 2, height / 2).strength(0.7));

  const inner = svg.select('g');

  // 缩放支持
  svg.call(d3.zoom().on('zoom', (e) => inner.attr('transform', e.transform)));

  // 定义箭头 marker
  const defs = svg.append('defs');
  defs.append('marker')
    .attr('id', 'arrow-contains')
    .attr('viewBox', '0 -5 10 10')
    .attr('refX', 20).attr('refY', 0)
    .attr('markerWidth', 6).attr('markerHeight', 6)
    .attr('orient', 'auto')
    .append('path').attr('d', 'M0,-5L10,0L0,5').attr('fill', '#666');
  defs.append('marker')
    .attr('id', 'arrow-enables')
    .attr('viewBox', '0 -5 10 10')
    .attr('refX', 20).attr('refY', 0)
    .attr('markerWidth', 8).attr('markerHeight', 8)
    .attr('orient', 'auto')
    .append('path').attr('d', 'M0,-5L10,0L0,5').attr('fill', '#cc7af0');

  // 绘制 links
  const link = inner.append('g').selectAll('line')
    .data(links).enter().append('line')
    .attr('class', (d) => `sitemap-link link-${d.kind}`)
    .attr('marker-end', (d) => `url(#arrow-${d.kind})`);

  // 绘制 nodes
  const node = inner.append('g').selectAll('g')
    .data(nodes).enter().append('g')
    .attr('class', (d) => {
      let cls = `sitemap-svg-node node-${d.kind}`;
      if (d.kind === 'endpoint') cls += d.hasVuln ? ' endpoint-vuln' : ' endpoint-clean';
      if (d.kind === 'finding') cls += ` severity-${d.severity}`;
      return cls;
    })
    .call(d3.drag()
      .on('start', (e, d) => { if (!e.active) simulation.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
      .on('drag', (e, d) => { d.fx = e.x; d.fy = e.y; })
      .on('end', (e, d) => { if (!e.active) simulation.alphaTarget(0); d.fx = null; d.fy = null; }));

  node.append('circle').attr('r', (d) => nodeRadius(d));
  node.append('text')
    .attr('dy', (d) => nodeRadius(d) + 12)
    .attr('text-anchor', 'middle')
    .text((d) => truncate(d.name, d.kind === 'finding' ? 30 : 20));
  node.append('title').text((d) => {
    if (d.kind === 'finding') return `${d.severity}: ${d.name}${d.cwe_id ? '\n' + d.cwe_id : ''}`;
    if (d.kind === 'endpoint') return `${d.method || ''} ${d.path}\n${d.findings.length} 漏洞`;
    return `${d.kind}: ${d.name}`;
  });

  // simulation tick
  simulation.on('tick', () => {
    link
      .attr('x1', (d) => d.source.x).attr('y1', (d) => d.source.y)
      .attr('x2', (d) => d.target.x).attr('y2', (d) => d.target.y);
    node.attr('transform', (d) => `translate(${d.x},${d.y})`);
  });
}

// 节点半径按 kind 区分（domain 最大，finding 最小）
function nodeRadius(d) {
  switch (d.kind) {
    case 'domain': return 18;
    case 'endpoint': return d.hasVuln ? 12 : 10;
    case 'finding': return 7;
    default: return 8;
  }
}

// 节点目标径向距离（domain 居中，endpoint 第 1 圈，finding 第 2 圈）
// forceRadial 按这个值把节点拉到对应同心圆上
function layerRadius(d) {
  switch (d.kind) {
    case 'domain': return 0;
    case 'endpoint': return 160;
    case 'finding': return 300;
    default: return 200;
  }
}

/**
 * 递归访问树节点。
 * @param {object} node
 * @param {(node:object) => void} fn
 */
function walkTree(node, fn) {
  if (!node) return;
  fn(node);
  for (const c of (node.children || [])) walkTree(c, fn);
}

/**
 * 从 sitemap 树扁平化抽取所有 findings 列表，渲染到右侧 Findings tab。
 * @param {object} root domain 根节点
 */
function renderFindings(root) {
  const ul = $('#panel-findings');
  ul.innerHTML = '';
  const findings = [];
  walkTree(root, (node) => {
    if (node.kind === 'endpoint' && node.findings) {
      for (const f of node.findings) {
        findings.push({ ...f, endpointPath: node.path, endpointName: node.name, method: node.method });
      }
    }
  });
  if (findings.length === 0) {
    ul.innerHTML = '<li class="empty" style="text-align:center;color:var(--text-muted)">暂无 finding</li>';
    return;
  }
  for (const f of findings) {
    const sev = (f.severity || 'info').toLowerCase();
    const li = document.createElement('li');
    li.innerHTML = `
      <div class="finding-title">${escapeHtml(f.summary)}</div>
      <div class="finding-meta">
        <span class="severity-badge severity-${escapeHtml(sev)}">${escapeHtml(f.severity || 'info')}</span>
        <span>${escapeHtml(f.method || '')} ${escapeHtml(f.endpointPath || '')}</span>
        ${f.cwe_id ? `<span class="finding-cwe">${escapeHtml(f.cwe_id)}</span>` : ''}
      </div>
    `;
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
 * 切换主视图：Sitemap（攻击面树）/ LLM（调用审计）/ Tasks（agent run 树）。
 * @param {'sitemap'|'llm'|'tasks'} view
 */
function switchView(view) {
  const sitemapView = $('#view-sitemap');
  const llmView = $('#view-llm');
  const tasksView = $('#view-tasks');
  const btnSitemap = $('#btn-view-sitemap');
  const btnLlm = $('#btn-view-llm');
  const btnTasks = $('#btn-view-tasks');

  sitemapView.classList.add('hidden');
  llmView.classList.add('hidden');
  tasksView.classList.add('hidden');
  btnSitemap.classList.remove('active');
  btnLlm.classList.remove('active');
  btnTasks.classList.remove('active');

  const eid = $('#select-eid').value || localStorage.getItem(STORAGE_KEYS.eid);
  const apikey = $('#input-apikey').value;

  if (view === 'llm') {
    llmView.classList.remove('hidden');
    btnLlm.classList.add('active');
    if (eid && apikey) loadInvocations(eid, apikey).catch((err) => console.warn('invocations load failed:', err));
  } else if (view === 'tasks') {
    tasksView.classList.remove('hidden');
    btnTasks.classList.add('active');
    if (eid && apikey) loadAgentRuns(eid, apikey).catch((err) => console.warn('agent_runs load failed:', err));
  } else {
    sitemapView.classList.remove('hidden');
    btnSitemap.classList.add('active');
  }
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// ---------- LLM invocation 审计（按 hunter_id 分组）----------

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

  const groupsHtml = data.groups.map((g) => renderInvocationGroup(g)).join('');
  panel.innerHTML = summary + groupsHtml;
}

function renderInvocationGroup(group) {
  const arid = group.hunter_id || 'unassigned';
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
        <dt>hunter_id</dt><dd>${escapeHtml(inv.hunter_id || '(null)')}</dd>
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

  // 按 commander_id 拼树
  const byId = {};
  const roots = [];
  for (const r of runs) byId[r.id] = { ...r, children: [] };
  for (const r of runs) {
    if (r.commander_id && byId[r.commander_id]) byId[r.commander_id].children.push(byId[r.id]);
    else roots.push(byId[r.id]);
  }

  panel.innerHTML = `
    <div class="tasks-header">total=${data.total} · roots=${roots.length}</div>
    <ul class="task-tree">${roots.map(renderTaskNode).join('')}</ul>
  `;
}

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
  if (ep && typeof ep === 'object') {
    if (ep.brief) briefSnippet = ` · ${ep.brief.slice(0, 80)}`;
    else if (ep.url) briefSnippet = ` · ${ep.method || ''} ${String(ep.url).slice(0, 70)}`;
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

// ---------- 启动 ----------

init();
