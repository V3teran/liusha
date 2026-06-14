<script setup lang="ts">
// 对话头像：agent=鲨齿剑 logo，user=同款深底圆+红剪影。
// 两者同色系深底圆，双主题（深/浅）下都是清晰一致的深色圆头像，风格统一。
defineProps<{ who: 'user' | 'agent' }>()
</script>

<template>
  <img
    class="avatar"
    :class="who"
    :src="who === 'user' ? '/avatar-user.svg' : '/logo.svg'"
    :alt="who === 'user' ? '我' : 'agent'"
    width="30"
    height="30"
  />
</template>

<style scoped>
.avatar {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  flex-shrink: 0;
  box-shadow: 0 2px 8px -2px rgba(220, 38, 38, 0.45);
  /* 深底圆固定，不随主题变；浅色主题下与背景对比清晰 */
}
</style>
