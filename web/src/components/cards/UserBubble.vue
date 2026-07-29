<script setup lang="ts">
defineProps<{ content: string }>()
</script>
<template>
  <div class="card user" data-card="user">{{ content }}</div>
</template>
