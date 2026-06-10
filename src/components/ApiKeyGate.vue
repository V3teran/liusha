<script setup lang="ts">
// API key 门：未鉴权时先收 X-API-Key，存入 sessionStorage 后放行。
import { ref } from 'vue'
import { getApiKey, setApiKey } from '../api/client'
const emit = defineEmits<{ ready: [] }>()
const key = ref(getApiKey())
function save() {
  setApiKey(key.value)
  if (key.value) emit('ready')
}
</script>
<template>
  <div class="gate">
    <input v-model="key" type="password" placeholder="X-API-Key" @keyup.enter="save" />
    <button @click="save">进入</button>
  </div>
</template>
