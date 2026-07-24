<script setup lang="ts">
// 执行图页：选 active owner → 解析会话（思维链来源）→ 拉执行图 → G6 分层 DAG 渲染。
// 节点：想(reasoning)/做+得(action)/漏洞(finding)/派(agent)；边：flow 实线、depends_on 虚线。
// 布局 antv-dagre（自上而下）；点节点弹详情。canvas 渲染，颜色取自 CSS 变量（主题色）+ severityColor。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Graph, NodeEvent, CanvasEvent } from '@antv/g6'
import OwnerPicker from '../components/OwnerPicker.vue'
import { getAttackGraph, getMilestones, listConversations, listMessages } from '../api/client'
import type { AttackGraph, AttackGraphNode, Milestone } from '../api/types'
import { severityColor } from '../lib/severity'
import { agentAccent } from '../lib/agentColor'

const owner = ref('')
const data = ref<AttackGraph | null>(null)
const loading = ref(false)
const error = ref('')
const selected = ref<AttackGraphNode | null>(null)
const contentByRef = ref<Record<string, string>>({}) // message id → 完整原文（点节点钻取）
const live = ref(true) // 实时轮询开关（扫描过程中观测）
const simplified = ref(true) // 成果优先：默认只显示通向漏洞的主干路径，折叠死路探索（默认开，大图才可读）
const expandedAnchors = ref<Set<string>>(new Set()) // 已展开的折叠段（按最近保留祖先 id；'' = 开场侦察段）
const currentConv = ref('')
const milestones = ref<Milestone[]>([]) // 里程碑摘要（按需 LLM 生成）
const milestonesLoading = ref(false)
const milestonesErr = ref('')

// 按需拉里程碑（LLM 调用，较慢）；agent→颜色映射与图节点一致。
async function loadMilestones() {
  if (!owner.value || !currentConv.value) return
  milestonesLoading.value = true
  milestonesErr.value = ''
  try {
    milestones.value = await getMilestones(owner.value, currentConv.value)
  } catch (e) {
    milestonesErr.value = e instanceof Error ? e.message : '生成失败'
  } finally {
    milestonesLoading.value = false
  }
}
// 里程碑卡按 agent 角色着色——复用全站单一真相源 agentColor.ts（会话卡/执行图同 agent 同色）。
function agentColor(a: string): string {
  return agentAccent(a).accent
}
let pollTimer: ReturnType<typeof setInterval> | null = null
let lastSig = '' // 上次图签名（节点+边数），变化检测防无谓重渲染
let lastContentSeq = 0 // 已拉取原文的最大 seq（增量拉新）

const nodes = computed<AttackGraphNode[]>(() => data.value?.nodes ?? [])
// 选中节点的完整原文（reasoning 节点 = 完整推理；缺失则空）。
const fullContent = computed(() => {
  const r = selected.value?.ref
  return r ? (contentByRef.value[r] ?? '') : ''
})

// immediate：owner 由 OwnerPicker 持久化恢复时，进页面即加载（否则有选中值却空白）。
// owner='' 时 load 自身 return，无害。
watch(owner, load, { immediate: true })
async function load() {
  selected.value = null
  milestones.value = [] // 换 owner 清空旧摘要
  milestonesErr.value = ''
  if (!owner.value) {
    data.value = null
    return
  }
  loading.value = true
  error.value = ''
  data.value = null
  try {
    // owner → conv：找 scan_id === owner 的会话（思维链来源）；无会话不致命，只出成果链。
    let conv = ''
    try {
      const convs = await listConversations()
      conv = convs.find((c) => c.ScanID === owner.value)?.ID ?? ''
    } catch {
      conv = ''
    }
    currentConv.value = conv
    contentByRef.value = {}
    lastContentSeq = 0
    data.value = await getAttackGraph(owner.value, conv)
    lastSig = sig(data.value)
    if (conv) void fetchContents(conv) // 异步填原文，不阻塞图渲染
    if (live.value) startPoll()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

// 增量拉会话消息（仅 seq > lastContentSeq 的新消息），并入 message id → 内容映射，供钻取看原文。
async function fetchContents(conv: string) {
  const m = { ...contentByRef.value }
  let after = lastContentSeq
  for (let i = 0; i < 50; i++) {
    let batch
    try {
      batch = await listMessages(conv, after)
    } catch {
      break
    }
    if (!batch.length) break
    for (const msg of batch) {
      m[msg.ID] = msg.Content
      if (msg.Seq > lastContentSeq) lastContentSeq = msg.Seq
    }
    const last = batch[batch.length - 1].Seq
    if (last === after) break
    after = last
  }
  contentByRef.value = m
}

// sig 是图的轻量签名（节点+边数）；变化检测——无变化不重渲染（防闪烁）。
function sig(g: AttackGraph | null): string {
  return g ? `${g.nodes.length}:${g.edges.length}` : ''
}

// poll 轮询重拉图；仅签名变化才更新（扫描新增节点时刷新，空闲时静默）。
async function poll() {
  if (!owner.value || !live.value) return
  let g: AttackGraph
  try {
    g = await getAttackGraph(owner.value, currentConv.value)
  } catch {
    return
  }
  if (sig(g) === lastSig) return
  lastSig = sig(g)
  data.value = g
  if (currentConv.value) void fetchContents(currentConv.value) // 增量拉新原文
}

function startPoll() {
  stopPoll()
  pollTimer = setInterval(poll, 3000)
}
function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

watch(live, (v) => (v ? startPoll() : stopPoll()))
watch(simplified, () => {
  expandedAnchors.value = new Set() // 切换成果优先/完整时重置已展开的折叠段
  renderGraph()
})

function kindLabel(k: string): string {
  return k === 'reasoning' ? '想' : k === 'action' ? '做' : k === 'agent' ? '派' : '漏洞'
}

// ===== G6 渲染 =====
const canvasEl = ref<HTMLDivElement | null>(null)
let graph: Graph | null = null

// canvas 用不了 CSS 变量，构建时读一次主题色（切主题需重进页面，可接受）。
function readColors() {
  const s = getComputedStyle(document.documentElement)
  const v = (name: string, fb: string) => s.getPropertyValue(name).trim() || fb
  return {
    text: v('--text', '#cfd6e4'),
    border: v('--border', '#39414f'),
    accent: v('--accent', '#3b82f6'),
    primary: v('--primary', '#3b82f6'),
  }
}

// 过程节点（●圆）配色——统一 Tailwind 400-500 调性，圆内靠颜色分想/做/死路（算法核对区分度足）。
// 漏洞用 severityColor（红橙黄灰蓝威胁色，★星），子代理用紫（⬡六边）——形状已分层级，颜色专管子类。
const REASONING_COLOR = '#38bdf8' // 想：sky 天蓝（冷静思考）
const ACTION_COLOR = '#34d399' // 做：emerald 翠绿（执行）
const AGENT_COLOR = '#a78bfa' // 派：violet 紫（结构边界）
const DEAD_FILL = '#64748b' // 死路：slate 石板灰（弱化无果尝试，灰=失败语义，不抢威胁红）

// toG6 把图转 G6 格式。collapse=true（成果优先，默认）时只保留「成果路径」——通向漏洞的
// 主干（on_path）+ 子代理泳道（agent）+ 全部漏洞（finding）；把死路/探索（!on_path 的想/做，
// 真实扫描里上千步的绝大多数）按"最近保留祖先"重连折叠——避免上千节点挤成无法阅读的巨图。
// 死路不丢：切到完整模式看全部轨迹，或点保留节点钻取原文。
function toG6(g: AttackGraph | null, collapse: boolean) {
  if (!g) return { nodes: [], edges: [] }
  const byId: Record<string, AttackGraphNode> = {}
  for (const n of g.nodes) byId[n.id] = n

  // 组合漏洞：有 depends_on 入边的 finding（前置漏洞 A+B 串成的更高危漏洞）→ 视觉强化（更大+金光环）。
  // 攻击链路的「终点」最有报告价值，应在图上最醒目。
  const chained = new Set(g.edges.filter((e) => e.type === 'depends_on').map((e) => e.to))

  // 成果优先：聚焦「成果骨架」——漏洞(finding) + 子代理边界(agent) + 关键动作（产出漏洞的
  // evidence 动作 / 失败的 error 动作）。思考(reasoning)与普通成功动作折叠：on_path 主干在
  // 线性思维链下仍有数百步（漏洞前每一步都算其祖先），故只留关键节点。看全部切「完整」，
  // 看某节点原文点它钻取。on_path 字段保留供完整模式给死路降权。
  const evidenceFrom = new Set(g.edges.filter((e) => e.type === 'evidence').map((e) => e.from))
  // 漏洞前的最后一步思考：每个 evidence 动作沿 parent 上溯找最近 reasoning，凑成 想→做→漏洞 小叙事。
  const keyReasoning = new Set<string>()
  for (const aid of evidenceFrom) {
    let p = byId[aid]?.parent_id ?? ''
    const seen = new Set<string>()
    while (p && !seen.has(p)) {
      seen.add(p)
      if (byId[p]?.kind === 'reasoning') {
        keyReasoning.add(p)
        break
      }
      p = byId[p]?.parent_id ?? ''
    }
  }
  const kept = (n: AttackGraphNode) =>
    !collapse ||
    n.kind === 'finding' ||
    n.kind === 'agent' ||
    (n.kind === 'action' && (evidenceFrom.has(n.id) || n.status === 'error')) ||
    keyReasoning.has(n.id)
  const keptIds = new Set(g.nodes.filter(kept).map((n) => n.id))

  // 向上追溯到最近的保留祖先（折叠掉的 action 链跳过）。
  const nearestKept = (id: string): string => {
    let p = byId[id]?.parent_id ?? ''
    const seen = new Set<string>()
    while (p && !keptIds.has(p) && !seen.has(p)) {
      seen.add(p)
      p = byId[p]?.parent_id ?? ''
    }
    return keptIds.has(p) ? p : ''
  }

  // 占位节点 id 前缀（折叠段 → 可展开占位）。
  const phId = (a: string) => (a === '' ? '__ph_root__' : `__ph_${a}`)

  type G6Node = { id: string; data: Record<string, unknown> }
  type G6Edge = { id: string; source: string; target: string; data: { type: string } }
  const nodes: G6Node[] = []
  const edges: G6Edge[] = []
  const seenEdge = new Set<string>()
  let i = 0
  const addEdge = (src: string, tgt: string, type: string) => {
    if (!src || !tgt || src === tgt) return
    const k = `${src}->${tgt}:${type}`
    if (seenEdge.has(k)) return
    seenEdge.add(k)
    edges.push({ id: `e${i++}`, source: src, target: tgt, data: { type } })
  }

  // 完整模式：全节点（死路 dim 降权）+ 原边，不折叠。
  if (!collapse) {
    for (const n of g.nodes) {
      nodes.push({
        id: n.id,
        data: { kind: n.kind, label: n.title, status: n.status ?? '', severity: n.severity ?? '', dim: n.on_path !== true, collapsed: false, chained: chained.has(n.id) },
      })
    }
    for (const e of g.edges) addEdge(e.from, e.to, e.type)
    return { nodes, edges }
  }

  // 成果优先：折叠段→可展开占位（渐进披露，对齐 CAI/Strix）；开场侦察段（anchor='' 无保留祖先）
  // →根占位「🎯 侦察与初始访问」，给叙事一个头（修复"上来就一通派发"的突兀）。
  const exp = expandedAnchors.value
  const visible = (id: string) => keptIds.has(id) || exp.has(nearestKept(id))
  // 每段折叠节点数：始终统计（不因展开跳过）——占位节点常驻作 toggle 锚点，展开后变「▾ 收起」再点收回。
  const hiddenCount: Record<string, number> = {}
  for (const n of g.nodes) {
    if (keptIds.has(n.id)) continue
    const a = nearestKept(n.id)
    hiddenCount[a] = (hiddenCount[a] ?? 0) + 1
  }

  for (const n of g.nodes) {
    if (!visible(n.id)) continue
    nodes.push({
      id: n.id,
      data: { kind: n.kind, label: n.title, status: n.status ?? '', severity: n.severity ?? '', dim: false, collapsed: false, chained: chained.has(n.id) },
    })
  }
  for (const [a, count] of Object.entries(hiddenCount)) {
    const opening = a === ''
    const isExpanded = exp.has(a)
    // 展开态：「▾ 收起 N 步」；折叠态：开场「🎯 侦察与初始访问 · N 步 ▸」/ 其余「▸ 探索 N 步」。
    const label = isExpanded
      ? `▾ 收起 ${count} 步`
      : opening
        ? `🎯 侦察与初始访问 · ${count} 步 ▸`
        : `▸ 探索 ${count} 步`
    nodes.push({
      id: phId(a),
      data: { kind: 'collapsed', label, collapsed: true, expanded: isExpanded, anchor: a },
    })
  }

  // flow 骨干：parent_id 重建，父隐藏 → 连到父所在折叠段占位。
  for (const n of g.nodes) {
    if (!visible(n.id)) continue
    const p = n.parent_id ?? ''
    if (!p) continue
    if (visible(p)) addEdge(p, n.id, 'flow')
    else addEdge(phId(nearestKept(p)), n.id, 'flow')
  }
  for (const a of Object.keys(hiddenCount)) {
    // 占位挂 anchor 下（折叠态="展开"入口；展开态="收起"按钮，与展开出的真实节点平级）。
    // anchor='' 开场段无保留祖先 → 占位无父，自然成图根。
    if (a !== '') addEdge(a, phId(a), 'flow')
  }
  // 成果边（evidence/depends_on）：两端可见才连。
  for (const e of g.edges) {
    if (e.type === 'flow') continue
    if (visible(e.from) && visible(e.to)) addEdge(e.from, e.to, e.type)
  }
  return { nodes, edges }
}

async function renderGraph() {
  if (!graph) return
  graph.setData(toG6(data.value, simplified.value))
  await graph.render()
  // 思维链是长链：只横向适配、纵向保持节点可读大小（纵向超出靠滚动/拖动），
  // 而非 autoFit 把超高图整体压成细线。
  await graph.fitView({ when: 'overflow', direction: 'x' })
}

onMounted(() => {
  if (!canvasEl.value) return
  const C = readColors()

  graph = new Graph({
    container: canvasEl.value,
    autoResize: true,
    layout: { type: 'antv-dagre', rankdir: 'TB', nodesep: 18, ranksep: 28 },
    behaviors: ['zoom-canvas', 'drag-canvas', 'drag-element'],
    node: {
      // 形状表「3 个信息层级」（不按细类，避免形状过多视觉嘈杂）：
      //   成果 ★星=漏洞 / 结构 ⬡六边=子代理 / 过程 ●圆=想·做·死路（圆内靠颜色分子类）。
      // 颜色另由 style.fill 表达层级内子类（过程：青想/绿做/灰死路；漏洞：severity 威胁色）。
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      type: (d: any) => {
        const kind = d.data?.kind
        if (kind === 'finding') return 'star'
        if (kind === 'agent') return 'hexagon'
        return 'circle' // reasoning / action(含 error 死路) / collapsed 占位 → 统一圆
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      style: (d: any) => {
        const kind = d.data?.kind
        // 折叠占位节点：点击 toggle 展开/收起该探索段。
        // 折叠态=虚线空心（「▸ 展开」提示）；展开态=实心淡填充（「▾ 收起」按钮，提示可点回）。
        if (kind === 'collapsed') {
          const isExpanded = d.data?.expanded === true
          return {
            size: 20,
            fill: isExpanded ? 'rgba(176,124,255,0.18)' : 'transparent',
            stroke: AGENT_COLOR,
            lineWidth: 1.5,
            lineDash: isExpanded ? undefined : [3, 3],
            labelText: d.data?.label ?? '',
            labelFill: C.text,
            labelFontSize: 11,
            labelPlacement: 'right',
            labelMaxLines: 2,
            labelWordWrap: true,
            labelMaxWidth: 160,
            labelBackground: true,
            labelBackgroundFill: 'rgba(0,0,0,0.45)',
            labelBackgroundRadius: 3,
            labelPadding: [1, 4],
          }
        }
        const isErr = d.data?.status === 'error'
        const fill =
          kind === 'finding'
            ? severityColor[d.data?.severity] ?? '#94a3b8'
            : kind === 'reasoning'
              ? REASONING_COLOR
              : kind === 'agent'
                ? AGENT_COLOR
                : isErr
                  ? DEAD_FILL
                  : ACTION_COLOR
        // 组合漏洞（有 depends_on 入边，攻击链终点）：更大 + 金光环描边 + 投影，全图最醒目。
        const isChained = kind === 'finding' && d.data?.chained === true
        return {
          // star 视觉占面积小，放大到 38 突出漏洞；组合漏洞再放大到 50；其余形状 24。
          size: kind === 'finding' ? (isChained ? 50 : 38) : 24,
          opacity: d.data?.dim ? 0.28 : 1, // 完整模式死路淡显（dim），主干/成果亮
          fill,
          // 组合漏洞金光环；死路圆描深 slate 边（弱标失败，红留给 critical）；其余浅边。
          stroke: isChained ? '#fbbf24' : isErr ? '#475569' : 'rgba(255,255,255,0.18)',
          lineWidth: isChained ? 3 : 1,
          shadowColor: isChained ? 'rgba(251,191,36,0.6)' : undefined,
          shadowBlur: isChained ? 12 : 0,
          labelText: d.data?.label ?? '',
          labelFill: C.text,
          labelFontSize: 11,
          labelPlacement: 'right',
          labelMaxLines: 2,
          labelWordWrap: true,
          labelMaxWidth: 130,
          labelBackground: true,
          labelBackgroundFill: 'rgba(0,0,0,0.45)',
          labelBackgroundRadius: 3,
          labelPadding: [1, 4],
        }
      },
    },
    edge: {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      style: (d: any) => {
        const t = d.data?.type
        // flow 主干灰实线(骨架,弱)；depends_on 金虚线(漏洞→漏洞攻击链,最重要,醒目)；
        // evidence 洋红虚线(动作→漏洞证据指向)。两条成果边都避开 severity 红橙黄,且彼此区分。
        const stroke = t === 'depends_on' ? '#fbbf24' : t === 'evidence' ? '#d946ef' : C.border
        return {
          stroke,
          lineWidth: t === 'flow' ? 1.5 : 2,
          lineDash: t === 'flow' ? undefined : [4, 4],
          endArrow: true,
        }
      },
    },
  })

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  graph.on(NodeEvent.CLICK, (evt: any) => {
    const id = evt.target?.id
    // 占位节点：toggle 该折叠段（展开 ⇄ 收起，渐进披露），不钻取原文。
    if (typeof id === 'string' && id.startsWith('__ph_')) {
      const anchor = id === '__ph_root__' ? '' : id.slice('__ph_'.length)
      const s = new Set(expandedAnchors.value)
      if (s.has(anchor)) s.delete(anchor)
      else s.add(anchor)
      expandedAnchors.value = s
      renderGraph()
      return
    }
    selected.value = nodes.value.find((n) => n.id === id) ?? null
  })
  graph.on(CanvasEvent.CLICK, () => {
    selected.value = null
  })

  renderGraph()
})

watch(data, renderGraph)

onBeforeUnmount(() => {
  stopPoll()
  graph?.destroy()
  graph = null
})
</script>

<template>
  <div class="page">
    <div class="page-toolbar">
      <OwnerPicker v-model="owner" mode-filter="active" />
      <div v-if="nodes.length" class="legend">
        <span class="lg-group">过程</span>
        <span class="lg"><i class="sym reasoning">●</i>想</span>
        <span class="lg"><i class="sym action">●</i>做</span>
        <span class="lg"><i class="sym err">●</i>死路</span>
        <span class="lg-sep" />
        <span class="lg"><i class="sym agent">⬡</i>派发</span>
        <span class="lg"><i class="sym finding">★</i>漏洞</span>
      </div>
      <a-button v-if="nodes.length" size="small" :loading="milestonesLoading" style="margin-left: auto" @click="loadMilestones">
        里程碑摘要
      </a-button>
      <label v-if="owner" class="live-toggle">
        <a-switch v-model:checked="simplified" size="small" />成果优先
      </label>
      <label v-if="owner" class="live-toggle">
        <a-switch v-model:checked="live" size="small" />实时
      </label>
    </div>

    <div v-if="milestones.length || milestonesErr" class="milestone-bar">
      <span v-if="milestonesErr" class="state-err">⚠ {{ milestonesErr }}</span>
      <div v-for="m in milestones" :key="m.agent" class="ms-card" :style="{ borderLeftColor: agentColor(m.agent) }">
        <div class="ms-head">
          <span class="ms-agent" :style="{ color: agentColor(m.agent) }">{{ m.agent }}</span>
          <span class="ms-count">{{ m.node_count }} 步</span>
        </div>
        <p class="ms-summary">{{ m.summary }}</p>
      </div>
    </div>

    <div class="page-body graph-body">
      <div ref="canvasEl" class="graph-canvas" />

      <div v-if="loading" class="overlay state"><a-spin size="large" /></div>
      <div v-else-if="error" class="overlay state"><span class="state-err">⚠ {{ error }}</span></div>
      <div v-else-if="!owner" class="overlay state">请选择一个 active 扫描查看执行图</div>
      <div v-else-if="!nodes.length" class="overlay state">该扫描暂无执行图数据</div>

      <aside v-if="selected" class="detail">
        <header>
          <span class="d-kind" :class="`k-${selected.kind}`">{{ kindLabel(selected.kind) }}</span>
          <button class="d-close" @click="selected = null">✕</button>
        </header>
        <p class="d-title">{{ selected.title }}</p>
        <pre v-if="fullContent" class="d-content">{{ fullContent }}</pre>
        <div class="d-meta">
          <span v-if="selected.agent">子代理：{{ selected.agent }}</span>
          <span v-if="selected.severity">严重度：{{ selected.severity }}</span>
          <span v-if="selected.status === 'error'" class="d-err">死路 / 失败</span>
        </div>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.legend { display: flex; gap: 14px; align-items: center; margin-left: 16px; font-size: 12px; color: var(--muted); }
.live-toggle { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--muted); }

.milestone-bar {
  display: flex;
  gap: 10px;
  padding: 10px 16px;
  overflow-x: auto;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.ms-card {
  min-width: 240px;
  max-width: 340px;
  padding: 8px 12px;
  background: var(--surface, rgba(255, 255, 255, 0.03));
  border: 1px solid var(--border);
  border-left-width: 3px;
  border-radius: 6px;
}
.ms-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px; }
.ms-agent { font-size: 12px; font-weight: 700; }
.ms-count { font-size: 11px; color: var(--muted); }
.ms-summary { margin: 0; font-size: 13px; line-height: 1.5; color: var(--text); }
.lg { display: inline-flex; align-items: center; gap: 5px; }
.lg-group { font-size: 11px; color: var(--muted); opacity: 0.7; }
.lg-sep { width: 1px; height: 12px; background: var(--border); margin: 0 2px; }
.sym { font-size: 12px; line-height: 1; font-style: normal; }
.sym.reasoning { color: #38bdf8; }
.sym.action { color: #34d399; }
.sym.agent { color: #a78bfa; }
.sym.finding { color: #ef4444; }
.sym.err { color: #64748b; }

.graph-body { position: relative; flex: 1; min-height: 420px; }
.graph-canvas { position: absolute; inset: 0; }
.overlay {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  background: var(--bg, transparent);
  z-index: 2;
}

.detail {
  position: absolute;
  top: 12px;
  right: 12px;
  width: 280px;
  max-width: 60%;
  padding: 12px 14px;
  background: var(--surface, #1b212b);
  border: 1px solid var(--border);
  border-radius: 8px;
  z-index: 3;
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.35);
}
.detail header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
.d-kind { font-size: 11px; font-weight: 700; padding: 2px 8px; border-radius: 4px; color: #fff; }
.d-kind.k-reasoning { background: #38bdf8; }
.d-kind.k-action { background: #34d399; }
.d-kind.k-agent { background: #a78bfa; }
.d-kind.k-finding { background: #ef4444; }
.d-close { background: none; border: none; color: var(--muted); cursor: pointer; font-size: 13px; }
.d-title { font-size: 13px; color: var(--text); word-break: break-all; margin: 0 0 10px; }
.d-content {
  max-height: 260px;
  overflow: auto;
  margin: 0 0 10px;
  padding: 8px 10px;
  background: var(--bg, rgba(0, 0, 0, 0.2));
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
}
.d-meta { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--muted); }
.d-err { color: #e5484d; }
</style>
