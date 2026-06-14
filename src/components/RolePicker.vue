<script setup lang="ts">
// 角色选择：挂载时拉 /roles，按 mode 过滤（主动扫描页只列 active），默认选过滤后第一个。
import { computed, onMounted, ref } from 'vue'
import { listRoles } from '../api/client'
import type { Role } from '../api/types'

// mode 过滤：传入则只列该模式角色（主动扫描页传 'active'）；不传列全部。
const props = defineProps<{ mode?: string }>()
const roles = ref<Role[]>([])
const model = defineModel<string>()

const filtered = computed(() =>
  props.mode ? roles.value.filter((r) => r.mode === props.mode) : roles.value
)
// 已按 mode 过滤（主动扫描页只剩 active），label 不再赘述 mode 后缀。
const options = computed(() =>
  filtered.value.map((r) => ({ label: r.name, value: r.id }))
)
onMounted(async () => {
  roles.value = await listRoles()
  if (!model.value && filtered.value[0]) model.value = filtered.value[0].id
})
</script>

<template>
  <a-select
    v-model:value="model"
    :options="options"
    size="small"
    placeholder="选择场景角色"
    class="role-select"
  />
</template>

<style scoped>
.role-select { width: 240px; }
</style>
