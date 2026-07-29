<script setup lang="ts">
// 对话页：主从双栏——左 ConversationList + 右 ConversationDetail。mode 由路由 meta 决定
// （/conversations/active、/conversations/passive 共用本组件），active/passive 两个 tab
// 差异仅在列表文案/是否可发起新对话，详情区的 SSE/用量/状态/插话全在 ConversationDetail 内。
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import ConversationList from '../components/ConversationList.vue'
import ConversationDetail from '../components/ConversationDetail.vue'

const route = useRoute()
const mode = computed(() => route.meta.mode as 'active' | 'passive')
const currentConv = ref<string>('')
const convList = ref<InstanceType<typeof ConversationList> | null>(null)

// 主动 tab 下：从其他页跳来（?conv=xxx）时自动打开该会话（实时观察 + 插话）。
onMounted(() => {
  if (mode.value === 'active' && typeof route.query.conv === 'string' && route.query.conv) {
    currentConv.value = route.query.conv
  }
})
watch(
  () => route.query.conv,
  (c) => {
    if (mode.value === 'active' && typeof c === 'string' && c && c !== currentConv.value) {
      currentConv.value = c
    }
  },
)

function selectConv(convID: string) {
  currentConv.value = convID
}
function newConversation() {
  currentConv.value = '' // 空=新建态，ConversationDetail 露空状态 + 可发起 Composer（仅 active）
}
// 新会话建立：切到它 + 刷左列表（否则新会话不出现，要手动点 ↻）。
function onStarted(convID: string) {
  currentConv.value = convID
  convList.value?.refresh()
}
// 删除的若是当前打开会话→清空选中；删别的不影响当前视图。
function onConvDeleted(convID: string) {
  if (convID === currentConv.value) currentConv.value = ''
}

const heading = computed(() => (mode.value === 'passive' ? '流量批次' : undefined))
const emptyHint = computed(() =>
  mode.value === 'passive' ? '暂无流量分析会话——挂代理（passive 8888）收到流量后自动逐批分析' : undefined,
)
</script>

<template>
  <div class="conversations-view">
    <ConversationList
      ref="convList"
      :active-id="currentConv || undefined"
      :mode="mode"
      :allow-new="mode === 'active'"
      :heading="heading"
      :empty-hint="emptyHint"
      @select="selectConv"
      @new="newConversation"
      @deleted="onConvDeleted"
    />
    <ConversationDetail
      :conv-id="currentConv || undefined"
      :mode="mode"
      @started="onStarted"
      @running-changed="convList?.refresh()"
    />
  </div>
</template>

<style scoped>
.conversations-view {
  display: grid;
  grid-template-columns: 256px 1fr;
  height: 100%;
  min-height: 0;
}
.conversations-view :deep(.conv-list) { border-right: 1px solid var(--border); padding: 10px; }
</style>
