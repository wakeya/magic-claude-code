import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import LoginView from './views/LoginView.vue'
import DashboardView from './views/DashboardView.vue'
import './styles/main.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: LoginView },
    {
      path: '/providers/:providerId/usage',
      name: 'provider-usage',
      redirect: (to) => ({
        path: '/',
        query: { tab: 'providers', usage_provider: String(to.params.providerId) },
      }),
    },
    { path: '/', name: 'dashboard', component: DashboardView },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async (to) => {
  if (to.path === '/login') return true
  try {
    const res = await fetch('/api/status')
    if (res.status === 401) return { name: 'login' }
    return true
  } catch {
    return { name: 'login' }
  }
})

const app = createApp(App)
app.use(router)
app.mount('#app')
