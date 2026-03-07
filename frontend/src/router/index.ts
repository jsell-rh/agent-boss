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
      path: '/:space',
      name: 'space',
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
  ],
})

export default router
