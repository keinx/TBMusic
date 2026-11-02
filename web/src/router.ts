import { createRouter, createWebHistory , RouteRecordRaw } from 'vue-router'
import { useAuth } from './stores/auth'

const routes: RouteRecordRaw[] = [
  { path: '/', name: 'home', component: () => import('./views/HomeView.vue') },
  { path: '/search', name: 'search', component: () => import('./views/SearchView.vue') },
  { path: '/hot', name: 'hot', component: () => import('./views/HotView.vue') },
  { path: '/random', name: 'random', component: () => import('./views/RandomView.vue') },
  { path: '/detail/:type/:source/:id', name: 'detail', component: () => import('./views/DetailView.vue') },
  { path: '/playlists', name: 'playlists', component: () => import('./views/PlaylistsView.vue') },
  { path: '/playlists/:id', name: 'playlist', component: () => import('./views/PlaylistDetailView.vue') },
  { path: '/blacklist', name: 'blacklist', component: () => import('./views/BlacklistView.vue') },
  { path: '/history', name: 'history', component: () => import('./views/HistoryView.vue') },
  { path: '/queue', name: 'queue', component: () => import('./views/QueueView.vue') },
  { path: '/my', name: 'my', component: () => import('./views/MyView.vue') },
  { path: '/users', name: 'users', component: () => import('./views/UsersView.vue'), meta: { admin: true } },
  { path: '/system', name: 'system', component: () => import('./views/SystemSettingsView.vue'), meta: { admin: true } },
  { path: '/settings', name: 'settings', component: () => import('./views/SettingsView.vue') },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  const auth = useAuth()
  if (!auth.loaded) {
    return auth.init().then(() => {
      if (to.meta.admin && !auth.isAdmin) return '/'
      return true
    })
  }
  if (to.meta.admin && !auth.isAdmin) return '/'
  return true
})
