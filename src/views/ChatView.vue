<script setup lang="ts">
// 渗透会话页（active）：主从双栏——左 ConversationList(仅 active) + 右 ConversationDetail(作战轨迹)。
// 本页只管「选哪个会话」（currentConv）与列表↔详情联动；详情区的 SSE/用量/状态/插话全在
// ConversationDetail 内，与流量分析页共用同一套骨架。
import { onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import ConversationList from '../components/ConversationList.vue'
import ConversationDetail from '../components/ConversationDetail.vue'

const route = useRoute()
const currentConv = ref<string>('')
const convList = ref<InstanceType<typeof ConversationList> | null>(null)

// 从流量分析页跳来（?conv=xxx）：自动打开该会话（实时观察 + 插话）。
onMounted(() => {
  if (typeof route.query.conv === 'string' && route.query.conv) currentConv.value = route.query.conv
})
watch(
  () => route.query.conv,
  (c) => {
    if (typeof c === 'string' && c && c !== currentConv.value) currentConv.value = c
  },
)

function selectConv(convID: string) {
  currentConv.value = convID
}
function newConversation() {
  currentConv.value = '' // 空=新建态，ConversationDetail 露空状态 + 可发起 Composer
}
// 新会话建立：切到它 + 刷左列表（否则新会话不出现，要手动点 ↻）。
function onStarted(convID: string) {
  currentConv.value = convID
  convList.value?.refresh()
}
// 删除的若是当前打开会话→回到新建态；删别的不影响当前视图。
function onConvDeleted(convID: string) {
  if (convID === currentConv.value) newConversation()
}
</script>

<template>
  <div class="chat-view">
    <ConversationList
      ref="convList"
      :active-id="currentConv || undefined"
      mode="active"
      @select="selectConv"
      @new="newConversation"
      @deleted="onConvDeleted"
    />
    <ConversationDetail
      :conv-id="currentConv || undefined"
      mode="active"
      @started="onStarted"
      @running-changed="convList?.refresh()"
    />
  </div>
</template>

<style scoped>
.chat-view {
  display: grid;
  grid-template-columns: 256px 1fr;
  height: 100%;
  min-height: 0;
}
.chat-view :deep(.conv-list) { border-right: 1px solid var(--border); padding: 10px; }
</style>
