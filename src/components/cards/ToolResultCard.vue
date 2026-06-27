<script setup lang="ts">
// 工具结果卡：状态点(成功/错误) + 工具名 + 耗时，折叠看美化结果；错误默认展开。按 agent 名着色。
import { computed, ref } from 'vue'
import { agentAccent, agentLabel } from '../../lib/agentColor'
const props = defineProps<{
  tool: string
  result: string
  durationMs: number
  err: string
  agentName?: string
  images?: string[] // 截图 data URI（browser_use 页面截图），缩略图展示、点击放大
}>()
const open = ref(!!props.err)
const hasImages = computed(() => (props.images?.length ?? 0) > 0)
const lightbox = ref<string>('') // 点击放大的截图 data URI
const accent = computed(() => agentAccent(props.agentName)) // 每个 agent 独立色
const pretty = computed(() => {
  const raw = props.err || props.result
  if (!raw) return ''
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
})
const preview = computed(() => {
  const s = (props.err || props.result || '').replace(/\s+/g, ' ').trim()
  return s.length > 64 ? s.slice(0, 64) + '…' : s
})
</script>

<template>
  <div
    class="tool-result"
    :class="{ 'has-agent': !!agentName }"
    :style="{ '--ag': accent.accent, '--ag-soft': accent.soft }"
    data-card="tool-result"
    :data-error="!!err"
  >
    <button class="head" :class="{ open }" @click="open = !open">
      <span class="caret">▸</span>
      <span class="dot" :class="{ err: !!err }" />
      <code class="tool">{{ tool }}</code>
      <span class="dur">{{ durationMs }}ms</span>
      <span v-if="agentName" class="agent">{{ agentLabel(agentName) }}</span>
      <span v-if="hasImages" class="cam" title="含截图">📷 {{ images!.length }}</span>
      <span v-if="!open" class="preview">{{ preview }}</span>
    </button>
    <pre v-if="open" class="out" :class="{ err: !!err }">{{ pretty }}</pre>
    <!-- 截图缩略图：始终展示（不随 open 折叠）——渗透取证的关键视觉证据，点击放大 -->
    <div v-if="hasImages" class="shots">
      <img
        v-for="(img, i) in images"
        :key="i"
        :src="img"
        class="shot"
        loading="lazy"
        alt="agent 页面截图"
        @click="lightbox = img"
      />
    </div>
    <div v-if="lightbox" class="lightbox" @click="lightbox = ''">
      <img :src="lightbox" alt="截图放大" />
    </div>
  </div>
</template>

<style scoped>
.tool-result { align-self: flex-start; max-width: 85%; }
.head {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 6px 12px;
  cursor: pointer;
  font-size: 12.5px;
  color: var(--text);
  text-align: left;
}
.head:hover { border-color: var(--border-strong); }
.tool-result[data-error='true'] .head { border-color: var(--sev-critical); }
.caret { color: var(--muted); transition: transform 0.15s; font-size: 11px; }
.head.open .caret { transform: rotate(90deg); }
.dot { width: 6px; height: 6px; border-radius: 50%; background: var(--success); flex-shrink: 0; }
.dot.err { background: var(--sev-critical); }
.agent {
  font-family: var(--mono);
  font-size: 10.5px;
  color: var(--muted);
  background: var(--surface-2);
  border-radius: 4px;
  padding: 0 6px;
  flex-shrink: 0;
}
/* 按 agent 名着色（--ag 由 inline style 注入）：左竖线 + chip 同色；状态点 dot 仍绿/红表成功失败，与 agent 色正交 */
.tool-result.has-agent .head { border-left: 2px solid var(--ag); }
.tool-result.has-agent .agent { color: var(--ag); background: var(--ag-soft); }
/* 截图相机标记 + 缩略图 + 放大 lightbox */
.cam {
  font-size: 10.5px;
  color: var(--accent);
  flex-shrink: 0;
}
.shots {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}
.shot {
  width: 160px;
  max-height: 110px;
  object-fit: cover;
  object-position: top;
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: zoom-in;
  transition: border-color var(--duration-fast, 150ms);
}
.shot:hover {
  border-color: var(--accent);
}
.lightbox {
  position: fixed;
  inset: 0;
  z-index: 100;
  background: rgba(0, 0, 0, 0.85);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: zoom-out;
  padding: 32px;
}
.lightbox img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  border-radius: 8px;
}
.tool { font-family: var(--mono); color: var(--muted); }
.dur { color: var(--muted); font-size: 11px; }
.preview { color: var(--muted); font-family: var(--mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.out {
  margin: 6px 0 0;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 10px;
  font-size: 12px;
  max-height: 280px;
  overflow: auto;
}
.out.err { color: var(--sev-critical); border-color: var(--sev-critical); }
</style>
