<script setup lang="ts">
// 角色选择：挂载时拉取 /roles，默认选中第一个。
import { onMounted, ref } from 'vue'
import { listRoles } from '../api/client'
import type { Role } from '../api/types'
const roles = ref<Role[]>([])
const model = defineModel<string>()
onMounted(async () => {
  roles.value = await listRoles()
  if (!model.value && roles.value[0]) model.value = roles.value[0].id
})
</script>
<template>
  <select v-model="model">
    <option v-for="r in roles" :key="r.id" :value="r.id">{{ r.name }}（{{ r.mode }}）</option>
  </select>
</template>
