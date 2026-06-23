import './assets/main.css'

import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import AppTooltip from './components/common/AppTooltip.vue'
import tooltipDirective from './directives/tooltip'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.directive('tooltip', tooltipDirective)
app.component('AppTooltip', AppTooltip)

app.mount('#app')
