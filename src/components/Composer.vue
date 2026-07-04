<script setup lang="ts">
// 发起器：选角色 + 写 brief。
// 有 convId 走追加（followUp），否则新建对话（startChat）并向上抛新对话 ID。
import { ref } from 'vue'
import { startChat, followUp } from '../api/client'
import { useConversationStore } from '../stores/conversation'
import RolePicker from './RolePicker.vue'

const props = defineProps<{ convId?: string; scanning?: boolean }>()
const store = useConversationStore()
const brief = ref('')
const roleID = ref('')
const busyMsg = ref('')
const sending = ref(false)
// appended 带「发送前 seq 快照」——api 落的 user 消息 seq 必 > 此，handleAppended 据此增量拉取，
// 不被 SSE 抢先推高的 store.lastSeq 跳过（修「追加 user 消息漏进 store → 步号不重置」竞态）。
const emit = defineEmits<{ started: [convID: string]; appended: [afterSeq: number]; stop: [] }>()

async function send() {
  if (!brief.value.trim() || sending.value) return
  // 追加场景下扫描进行中：前端直接拦（内联停止按钮已提示），不发出去等后端 busy 往返。
  if (props.convId && props.scanning) {
    busyMsg.value = '扫描进行中，先点停止再发'
    return
  }
  busyMsg.value = ''
  sending.value = true
  try {
    if (props.convId) {
      // followUp 前快照 seq——user 消息 seq 必 > 此（发送后才新增）；后续 SSE 推高 store.lastSeq 不影响此快照值。
      const beforeSeq = store.lastSeq
      const r = await followUp(props.convId, brief.value)
      brief.value = ''
      busyMsg.value = r.intent === 'qa' ? '正在回答…' : '已触发扫描'
      emit('appended', beforeSeq)
    } else {
      const { conversation_id } = await startChat(brief.value, roleID.value)
      brief.value = ''
      emit('started', conversation_id)
    }
  } catch (e) {
    const err = e as Error & { busy?: boolean }
    busyMsg.value = err.busy ? '扫描进行中，先点停止再发' : '发送失败'
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div class="composer">
    <div v-if="!convId" class="composer-top">
      <RolePicker v-model="roleID" mode="active" />
      <span class="composer-tip">选择场景，Cmd/Ctrl + Enter 发送</span>
    </div>
    <div class="composer-box" :class="{ 'is-scanning': convId && scanning }">
      <textarea
        v-model="brief"
        class="composer-input"
        :placeholder="
          convId && scanning
            ? '扫描进行中，先点停止再发追加指令…'
            : convId
              ? '追加指令（在同一目标上继续）…'
              : '描述要扫的目标 / 任务（URL、账号、测试方向）…'
        "
        @keydown.meta.enter="send"
        @keydown.ctrl.enter="send"
      />
      <div class="composer-actions">
        <span v-if="busyMsg" class="busy">{{ busyMsg }}</span>
        <!-- 扫描进行中：内联停止按钮（追加场景），停了才能发下一条 -->
        <button v-if="convId && scanning" class="stop" type="button" @click="emit('stop')">
          ■ 停止扫描
        </button>
        <button v-else class="send" :disabled="!brief.trim() || sending" @click="send">
          {{ sending ? '发送中…' : convId ? '追加' : '发起扫描' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.composer {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  border-top: 1px solid var(--border);
  background: var(--surface);
  flex-shrink: 0;
}
.composer-top { display: flex; align-items: center; gap: 12px; }
.composer-tip { font-size: 12px; color: var(--muted); }
.composer-box {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg);
  transition: border-color 0.15s;
}
.composer-box:focus-within { border-color: var(--primary); }
.composer-input {
  width: 100%;
  min-height: 56px;
  max-height: 200px;
  background: transparent;
  color: var(--text);
  border: none;
  padding: 12px 12px 0;
  font-family: inherit;
  font-size: 14px;
  line-height: 1.5;
  resize: vertical;
  outline: none;
}
.composer-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  padding: 8px 10px;
}
.busy { font-size: 12.5px; color: var(--muted); }
.send {
  background: var(--primary);
  color: #fff;
  border: none;
  border-radius: var(--radius);
  padding: 8px 18px;
  font-size: 13.5px;
  font-weight: 600;
  cursor: pointer;
}
.send:hover:not(:disabled) { background: var(--primary-hover); }
.send:disabled { opacity: 0.5; cursor: not-allowed; }
/* 扫描进行中：输入框整体降饱和度提示「此刻不可发」，内联停止按钮走危险色。 */
.composer-box.is-scanning { opacity: 0.7; }
.composer-box.is-scanning .composer-input { cursor: not-allowed; }
.stop {
  border: 1px solid var(--sev-critical);
  color: var(--sev-critical);
  background: transparent;
  border-radius: var(--radius);
  padding: 8px 16px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}
.stop:hover { background: color-mix(in srgb, var(--sev-critical) 14%, transparent); }
</style>
