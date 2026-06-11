import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { bootstrapApiKey } from './api/client'
import './style.css'

// 先尝试自动拉 dev key（无登录门），再挂载——失败也照常启动。
bootstrapApiKey().finally(() => {
  createApp(App).use(createPinia()).use(router).mount('#app')
})
