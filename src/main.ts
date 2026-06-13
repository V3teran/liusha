import { createApp } from 'vue'
import { createPinia } from 'pinia'
import Antd from 'ant-design-vue'
import 'ant-design-vue/dist/reset.css' // Ant 基础样式（在自定义 style.css 前，token 由后者+ConfigProvider 覆盖）
import App from './App.vue'
import { router } from './router'
import { bootstrapApiKey } from './api/client'
import './style.css'

// 先尝试自动拉 dev key（无登录门），再挂载——失败也照常启动。
bootstrapApiKey().finally(() => {
  createApp(App).use(createPinia()).use(router).use(Antd).mount('#app')
})
