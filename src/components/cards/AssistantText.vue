<script setup lang="ts">
defineProps<{ content: string }>()
</script>
<template>
  <div class="card assistant" data-card="assistant">{{ content }}</div>
</template>
