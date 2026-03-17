import { createRouter, createWebHistory } from 'vue-router'
import { defineComponent, h } from 'vue'

// Passthrough component — the actual rendering is handled by App.vue
// which reads route params via useRoute()
const Empty = defineComponent({ render: () => h('span') })

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'home',
      component: Empty,
    },
    {
      // Redirect /spaces/:name → /:name so external links and API URL patterns work
      path: '/spaces/:space',
      redirect: (to) => '/' + to.params.space,
    },
    {
      path: '/:space',
      name: 'space',
      component: Empty,
    },
    {
      path: '/:space/kanban',
      name: 'kanban',
      component: Empty,
    },
    {
      path: '/:space/conversations',
      name: 'conversations',
      component: Empty,
    },
    {
      path: '/:space/conversations/:conversationAgent',
      name: 'conversation',
      component: Empty,
    },
    {
      path: '/:space/:agent',
      name: 'agent',
      component: Empty,
    },
    {
      path: '/personas',
      name: 'personas',
      component: Empty,
    },
    {
      path: '/settings',
      name: 'settings',
      component: Empty,
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: Empty,
    },
  ],
})

export default router
