import { createRouter, createWebHistory } from 'vue-router'
import WorkspaceView from '@/views/WorkspaceView.vue'
import FlowView from '@/views/FlowView.vue'
import KanbanView from '@/views/KanbanView.vue'
import ChatView from '@/views/ChatView.vue'
import ArchivedTasksView from '@/views/ArchivedTasksView.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      redirect: '/kanban',
    },
    {
      path: '/chat/:id?',
      name: 'chat-with-id',
      component: ChatView,
    },
    {
      path: '/kanban',
      name: 'kanban',
      component: KanbanView,
    },
    {
      path: '/flows/:id',
      name: 'flow',
      component: FlowView,
    },
    {
      path: '/workspaces/new',
      name: 'create-workspace',
      component: WorkspaceView,
    },
    {
      path: '/workspaces/:id',
      name: 'workspace',
      component: WorkspaceView,
      props: true
    },
    {
      path: '/archived-tasks',
      name: 'archived-tasks',
      component: ArchivedTasksView,
    },
  ],
})

export default router
