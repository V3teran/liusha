<script setup lang="ts">
defineProps<{ result: string }>()
</script>
<template>
  <div class="card finding" data-card="finding">
    <span class="badge">FINDING</span>
    <span class="text">{{ result }}</span>
  </div>
</template>
