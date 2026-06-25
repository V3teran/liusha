<script setup lang="ts">
// 执行图页：选 active owner → 解析对话（思维链来源）→ 拉执行图 → G6 分层 DAG 渲染。
// 节点：想(reasoning)/做+得(action)/漏洞(finding)/派(agent)；边：flow 实线、depends_on 虚线。
// 布局 antv-dagre（自上而下）；点节点弹详情。canvas 渲染，颜色取自 CSS 变量（主题色）+ severityColor。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Graph, NodeEvent, CanvasEvent } from '@antv/g6'
import OwnerPicker from '../components/OwnerPicker.vue'
import { getAttackGraph, getMilestones, listConversations, listMessages } from '../api/client'
import type { AttackGraph, AttackGraphNode, Milestone } from '../api/types'
import { severityColor } from '../lib/severity'

const owner = ref('')
const data = ref<AttackGraph | null>(null)
const loading = ref(false)
const error = ref('')
const selected = ref<AttackGraphNode | null>(null)
const contentByRef = ref<Record<string, string>>({}) // message id → 完整原文（点节点钻取）
const live = ref(true) // 实时轮询开关（扫描过程中观测）
const simplified = ref(true) // 精简模式：折叠 action 细节，只看思路主干（默认开，大图才可读）
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
function agentColor(a: string): string {
  return a === 'orchestrator' ? '#b07cff' : a === 'reconnaissance' ? '#58a6ff' : a === 'exploitation' ? '#2bb673' : '#6e7681'
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
    // owner → conv：找 scan_id === owner 的对话（思维链来源）；无对话不致命，只出成果链。
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

// 增量拉对话消息（仅 seq > lastContentSeq 的新消息），并入 message id → 内容映射，供钻取看原文。
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
watch(simplified, renderGraph) // 切换精简/完整即重渲染

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
    accent: v('--accent', '#58a6ff'),
    primary: v('--primary', '#2bb673'),
  }
}

const AGENT_COLOR = '#b07cff'
const ERROR_COLOR = '#e5484d'

// toG6 把图转 G6 格式。simplified=true 时折叠 action 细节节点（512 个工具调用），
// 只留思路主干（想/派/漏洞），把边按"最近保留祖先"重连——避免 906 节点线性长链挤成细线。
// action 仍可在完整模式查看，或点保留节点钻取原文。
function toG6(g: AttackGraph | null, collapse: boolean) {
  if (!g) return { nodes: [], edges: [] }
  const byId: Record<string, AttackGraphNode> = {}
  for (const n of g.nodes) byId[n.id] = n

  const kept = (n: AttackGraphNode) => !collapse || n.kind !== 'action'
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

  const nodes = g.nodes.filter(kept).map((n) => ({
    id: n.id,
    data: { kind: n.kind, label: n.title, status: n.status ?? '', severity: n.severity ?? '' },
  }))

  const edges: { id: string; source: string; target: string; data: { type: string } }[] = []
  const seenEdge = new Set<string>()
  let i = 0
  for (const e of g.edges) {
    // 两端都保留 → 原样；折叠端 → 重连到最近保留祖先（消除断边）。
    const src = keptIds.has(e.from) ? e.from : nearestKept(e.from)
    const tgt = keptIds.has(e.to) ? e.to : nearestKept(e.to)
    if (!src || !tgt || src === tgt) continue
    const key = `${src}->${tgt}:${e.type}`
    if (seenEdge.has(key)) continue
    seenEdge.add(key)
    edges.push({ id: `e${i++}`, source: src, target: tgt, data: { type: e.type } })
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
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      style: (d: any) => {
        const kind = d.data?.kind
        const isErr = d.data?.status === 'error'
        const fill =
          kind === 'finding'
            ? severityColor[d.data?.severity] ?? '#6e7681'
            : kind === 'reasoning'
              ? C.accent
              : kind === 'agent'
                ? AGENT_COLOR
                : isErr
                  ? ERROR_COLOR
                  : C.primary
        return {
          size: kind === 'finding' ? 30 : 24,
          fill,
          stroke: isErr ? ERROR_COLOR : 'rgba(255,255,255,0.18)',
          lineWidth: 1,
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
        // flow 主干实线灰；depends_on 蓝虚线；evidence（动作→漏洞）红虚线
        const stroke = t === 'depends_on' ? C.accent : t === 'evidence' ? '#f85149' : C.border
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
        <span class="lg"><i class="dot reasoning" />想</span>
        <span class="lg"><i class="dot action" />做</span>
        <span class="lg"><i class="dot agent" />派</span>
        <span class="lg"><i class="dot finding" />漏洞</span>
        <span class="lg"><i class="dot err" />死路</span>
      </div>
      <a-button v-if="nodes.length" size="small" :loading="milestonesLoading" style="margin-left: auto" @click="loadMilestones">
        里程碑摘要
      </a-button>
      <label v-if="owner" class="live-toggle">
        <a-switch v-model:checked="simplified" size="small" />精简
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
.dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
.dot.reasoning { background: var(--accent); }
.dot.action { background: var(--primary); }
.dot.agent { background: #b07cff; }
.dot.finding { background: #f85149; }
.dot.err { background: #e5484d; }

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
.d-kind.k-reasoning { background: var(--accent); }
.d-kind.k-action { background: var(--primary); }
.d-kind.k-agent { background: #b07cff; }
.d-kind.k-finding { background: #f85149; }
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
