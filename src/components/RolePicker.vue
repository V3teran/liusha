<script setup lang="ts">
// 角色选择：挂载时拉 /roles，默认选第一个。用 Ant a-select 呈现（场景 + 模式）。
import { computed, onMounted, ref } from 'vue'
import { listRoles } from '../api/client'
import type { Role } from '../api/types'

const roles = ref<Role[]>([])
const model = defineModel<string>()
const options = computed(() =>
  roles.value.map((r) => ({ label: `${r.name} · ${r.mode}`, value: r.id }))
)
onMounted(async () => {
  roles.value = await listRoles()
  if (!model.value && roles.value[0]) model.value = roles.value[0].id
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
