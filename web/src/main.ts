import './assets/main.css'

import { createApp } from 'vue'
import { createPinia } from 'pinia'
import VueApexCharts from 'vue3-apexcharts'
import App from './App.vue'
import router from './router'

const app = createApp(App)

app.use(createPinia())
app.use(router)
// 全局注册 ApexCharts 组件（<apexchart>），供统计页等图表复用（任务 26.5）
app.use(VueApexCharts)

app.mount('#app')
