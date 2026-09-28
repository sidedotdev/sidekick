import { createRouter, createWebHistory } from 'vue-router'
import KanbanView from '@/views/KanbanView.vue'
import BlockedView from '@/views/BlockedView.vue'
import { isBlockedNow } from '@/lib/offHours'

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
      component: () => import('@/views/ChatView.vue'),
    },
    {
      path: '/kanban',
      name: 'kanban',
      component: KanbanView,
    },
    {
      path: '/projects',
      name: 'projects',
      component: () => import('@/views/ProjectsView.vue'),
    },
    {
      path: '/projects/new',
      name: 'project-new',
      component: () => import('@/views/ProjectFormView.vue'),
    },
    {
      path: '/projects/:id/edit',
      name: 'project-edit',
      component: () => import('@/views/ProjectFormView.vue'),
    },
    {
      path: '/flows/:id/intent',
      name: 'intent-canvas',
      component: () => import('@/views/IntentCanvasView.vue'),
    },
    {
      path: '/flows/:id',
      name: 'flow',
      component: () => import('@/views/FlowView.vue'),
    },
    {
      path: '/flows/:id/reset',
      name: 'flow-reset',
      component: () => import('@/views/WorkflowResetView.vue'),
    },
    {
      path: '/workspaces/new',
      name: 'create-workspace',
      component: () => import('@/views/WorkspaceView.vue'),
    },
    {
      path: '/workspaces/:id',
      name: 'workspace',
      component: () => import('@/views/WorkspaceView.vue'),
      props: true
    },
    {
      path: '/archived-tasks',
      name: 'archived-tasks',
      component: () => import('@/views/ArchivedTasksView.vue'),
    },
    {
      path: '/remote-control',
      name: 'remote-control',
      component: () => import('@/views/RemoteControlView.vue'),
    },
    {
      path: '/blocked',
      name: 'blocked',
      component: BlockedView,
    },
    {
      path: '/dev/evaldata',
      name: 'eval-data-validator',
      component: () => import('@/views/EvalDataValidatorView.vue'),
    },
  ],
})

router.beforeEach(async (to, _from, next) => {
  if (to.name === 'blocked') {
    next()
    return
  }

  const status = await isBlockedNow()
  if (status.blocked) {
    next({
      name: 'blocked',
      query: { redirect: to.fullPath },
    })
  } else {
    next()
  }
})

export default router