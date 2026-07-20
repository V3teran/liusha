<script setup lang="ts">
// 流量分析页（passive）：主从双栏——左 ConversationList(仅 passive，按 host 组织的流量批次) +
// 右 ConversationDetail(分析轨迹)。与渗透会话页同一套主从骨架，差异仅在：
//   · 左列表只列 passive 会话，无「+ 新对话」（passive 由代理流量驱动自动建会话，不手动发起）；
//   · 右侧空状态文案是「选一批流量看分析」而非「发起扫描」；Composer 仅在选中会话时露出（可插话追问）。
// 右侧渲染复用同一 TimelineThread：passive 是单 traffic-analysis 代理，脊柱自然扁平（无子代理缩进）。
import { ref } from 'vue'
import ConversationList from '../components/ConversationList.vue'
import ConversationDetail from '../components/ConversationDetail.vue'

const currentConv = ref<string>('')
const convList = ref<InstanceType<typeof ConversationList> | null>(null)

function selectConv(convID: string) {
  currentConv.value = convID
}
function onConvDeleted(convID: string) {
  if (convID === currentConv.value) currentConv.value = ''
}
</script>

<template>
  <div class="traffic-view">
    <ConversationList
      ref="convList"
      :active-id="currentConv || undefined"
      mode="passive"
      :allow-new="false"
      heading="流量批次"
      empty-hint="暂无流量分析会话——挂代理（passive 8888）收到流量后自动逐批分析"
      @select="selectConv"
      @deleted="onConvDeleted"
    />
    <ConversationDetail
      :conv-id="currentConv || undefined"
      mode="passive"
      @running-changed="convList?.refresh()"
    />
  </div>
</template>

<style scoped>
.traffic-view {
  display: grid;
  grid-template-columns: 256px 1fr;
  height: 100%;
  min-height: 0;
}
.traffic-view :deep(.conv-list) { border-right: 1px solid var(--border); padding: 10px; }
</style>
