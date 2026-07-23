import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import KanbanBoard from '../KanbanBoard.vue'
import VirtualTaskList from '../VirtualTaskList.vue'
import TaskModal from '../TaskModal.vue'
import { store } from '../../lib/store'
import type { FullTask, Project } from '../../lib/models'

describe('KanbanBoard', () => {
  const defaultProps = {
    tasks: [] as FullTask[],
    showGuidedOverlay: false
  }

  const stubFetchProjects = (projects: Project[]) => {
    vi.stubGlobal('fetch', vi.fn(() =>
      Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ projects }),
      } as Response)
    ))
  }

  const mountBoard = async (props: Partial<typeof defaultProps> = {}, projects: Project[] = []) => {
    stubFetchProjects(projects)
    const wrapper = mount(KanbanBoard, {
      props: { ...defaultProps, ...props },
      global: {
        stubs: {
          VirtualTaskList: true,
          TaskModal: true,
          ShortcutHint: true,
        },
      },
    })
    await flushPromises()
    return wrapper
  }

  beforeEach(() => {
    store.workspaceId = 'test-workspace-id'
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders no tasks when tasks prop is empty', async () => {
    const wrapper = await mountBoard()
    const lists = wrapper.findAllComponents(VirtualTaskList)
    expect(lists.length).toBe(3)
    lists.forEach(list => {
      expect(list.props().tasks.length).toBe(0)
    })
  })

  it('renders tasks in the correct columns', async () => {
    const tasks = [
      { id: '1', agentType: 'human', status: 'to_do'       },
      { id: '2', agentType: 'llm'  , status: 'in_progress' },
      { id: '3', agentType: 'none' , status: 'complete'    },
      { id: '4', agentType: 'llm'  , status: 'to_do'       },
    ] as FullTask[]
    const wrapper = await mountBoard({ tasks })
    const columns = wrapper.findAll('.kanban-column')
    expect(columns.length).toBe(3)
    expect(columns.at(0)!.findComponent(VirtualTaskList).props().tasks.length).toBe(1) // human
    expect(columns.at(1)!.findComponent(VirtualTaskList).props().tasks.length).toBe(2) // llm
    expect(columns.at(2)!.findComponent(VirtualTaskList).props().tasks.length).toBe(1) // none
  })

  it('displays tasks in descending order of id', async () => {
    const tasks = [
      { id: '1', agentType: 'human', status: 'drafting' },
      { id: '2', agentType: 'human', status: 'drafting' },
      { id: '3', agentType: 'human', status: 'drafting' },
    ] as FullTask[]

    const wrapper = await mountBoard({ tasks })

    const columns = wrapper.findAll('.kanban-column')
    const humanTasks = columns.at(0)!.findComponent(VirtualTaskList).props().tasks
    expect(humanTasks[0].id).toBe('3')
    expect(humanTasks[1].id).toBe('2')
    expect(humanTasks[2].id).toBe('1')
  })

  describe('project groups', () => {
    const projects = [
      { id: 'project_1', workspaceId: 'test-workspace-id', title: 'Alpha', priority: 'high' },
      { id: 'project_2', workspaceId: 'test-workspace-id', title: 'Beta', priority: 'none' },
    ] as Project[]

    it('groups tasks by project in fetched order above the everything else group', async () => {
      const tasks = [
        { id: '1', agentType: 'human', status: 'to_do', projectId: 'project_2' },
        { id: '2', agentType: 'llm'  , status: 'to_do', projectId: 'project_1' },
        { id: '3', agentType: 'human', status: 'to_do' },
        { id: '4', agentType: 'human', status: 'to_do', projectId: 'project_unknown' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)

      const groups = wrapper.findAll('.project-group')
      expect(groups.length).toBe(3)
      expect(groups.at(0)!.find('.project-group-header').text()).toBe('Alpha')
      expect(groups.at(1)!.find('.project-group-header').text()).toBe('Beta')
      expect(groups.at(2)!.find('.project-group-header').text()).toBe('Everything else')

      const tasksIn = (groupIndex: number) =>
        groups.at(groupIndex)!.findAllComponents(VirtualTaskList)
          .flatMap((list: VueWrapper) => list.props().tasks as FullTask[])
          .map((task: FullTask) => task.id)
      expect(tasksIn(0)).toEqual(['2'])
      expect(tasksIn(1)).toEqual(['1'])
      // Tasks without a project or with an unknown project fall into everything else
      expect(tasksIn(2).sort()).toEqual(['3', '4'])
    })

    it('renders projects with zero tasks as empty groups', async () => {
      const wrapper = await mountBoard({}, projects)
      const groups = wrapper.findAll('.project-group')
      expect(groups.length).toBe(3)
      groups.forEach(group => {
        group.findAllComponents(VirtualTaskList).forEach((list: VueWrapper) => {
          expect(list.props().tasks.length).toBe(0)
        })
      })
    })

    it('renders plain kanban columns with no group wrapper when there are no projects', async () => {
      const wrapper = await mountBoard()
      expect(wrapper.findAll('.project-group').length).toBe(0)
      expect(wrapper.find('.project-group-header').exists()).toBe(false)
      expect(wrapper.find('.kanban-column-group').exists()).toBe(true)
    })

    it('opens the task modal with the project preselected when adding from a project group', async () => {
      const wrapper = await mountBoard({}, projects)

      const groups = wrapper.findAll('.project-group')
      await groups.at(0)!.findAll('.new-task')
        .find(button => button.text().includes('Draft Task'))!
        .trigger('click')

      const modal = wrapper.findComponent(TaskModal)
      expect(modal.exists()).toBe(true)
      expect(modal.props('task')!.projectId).toBe('project_1')
      expect(modal.props('task')!.agentType).toBe('human')
    })

    it('opens the task modal without a project when adding from the everything else group', async () => {
      const wrapper = await mountBoard({}, projects)

      const groups = wrapper.findAll('.project-group')
      await groups.at(2)!.findAll('.new-task')
        .find(button => button.text().includes('Draft Task'))!
        .trigger('click')

      const modal = wrapper.findComponent(TaskModal)
      expect(modal.exists()).toBe(true)
      expect(modal.props('task')!.projectId).toBeUndefined()
    })

    it('shows the column headings once at the top instead of repeating them per group', async () => {
      const tasks = [
        { id: '1', agentType: 'human', status: 'to_do', projectId: 'project_1' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)

      const headings = wrapper.find('.board-column-headings')
      expect(headings.exists()).toBe(true)
      expect(headings.text()).toContain('You')
      expect(headings.text()).toContain('AI Sidekick')
      expect(headings.text()).toContain('Finished')

      const columnHeadingTexts = wrapper.findAll('.kanban-column h2').map(h => h.text())
      columnHeadingTexts.forEach(text => {
        expect(text).not.toContain('You')
        expect(text).not.toContain('AI Sidekick')
        expect(text).not.toContain('Finished')
      })
    })

    it('shows headings within the columns when there are no projects', async () => {
      const wrapper = await mountBoard()

      expect(wrapper.find('.board-column-headings').exists()).toBe(false)
      const columnHeadingTexts = wrapper.findAll('.kanban-column h2').map(h => h.text())
      expect(columnHeadingTexts.length).toBe(3)
      expect(columnHeadingTexts[0]).toContain('You')
      expect(columnHeadingTexts[1]).toContain('AI Sidekick')
      expect(columnHeadingTexts[2]).toContain('Finished')
    })

    it('renders empty project groups compactly, with no column headings', async () => {
      const wrapper = await mountBoard({}, projects)

      const group = wrapper.findAll('.project-group').at(0)!
      expect(group.find('.kanban-column-group').classes()).toContain('compact')
      expect(group.find('.kanban-column h2').exists()).toBe(false)
    })

    it('does not render groups with tasks compactly', async () => {
      const tasks = [
        { id: '1', agentType: 'human', status: 'to_do', projectId: 'project_1' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)

      const group = wrapper.findAll('.project-group').at(0)!
      expect(group.find('.kanban-column-group').classes()).not.toContain('compact')
    })

    it('shows the project icon in project group headers', async () => {
      const wrapper = await mountBoard({}, projects)

      const groups = wrapper.findAll('.project-group')
      expect(groups.at(0)!.find('.project-group-toggle .project-group-icon').exists()).toBe(true)
      // The everything else group is not a project, so it has no icon
      expect(groups.at(2)!.find('.project-group-toggle .project-group-icon').exists()).toBe(false)
    })
  })

  describe('collapse and expand', () => {
    const projects = [
      { id: 'project_1', workspaceId: 'test-workspace-id', title: 'Alpha', priority: 'high' },
      { id: 'project_2', workspaceId: 'test-workspace-id', title: 'Beta', priority: 'none' },
    ] as Project[]

    // isVisible() is unreliable with multiple sibling groups, so assert on the
    // inline style that v-show controls
    const isGroupBodyVisible = (wrapper: VueWrapper, index: number) => {
      const style = wrapper.findAll('.project-group').at(index)!
        .find('.kanban-column-group').attributes('style') ?? ''
      return !style.includes('display: none')
    }

    it('auto-collapses projects with no actionable tasks and expands the rest', async () => {
      const tasks = [
        { id: '1', agentType: 'human', status: 'drafting' , projectId: 'project_1' },
        { id: '2', agentType: 'human', status: 'blocked'  , projectId: 'project_1' },
        { id: '3', agentType: 'llm'  , status: 'in_review', projectId: 'project_1' },
        { id: '4', agentType: 'llm'  , status: 'to_do'    , projectId: 'project_2' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)

      expect(isGroupBodyVisible(wrapper, 0)).toBe(true)
      expect(isGroupBodyVisible(wrapper, 1)).toBe(false)
    })

    it('auto-collapses a project whose tasks are all finished', async () => {
      const tasks = [
        { id: '1', agentType: 'none', status: 'complete', projectId: 'project_1' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)

      expect(isGroupBodyVisible(wrapper, 0)).toBe(false)
    })

    it('auto-collapses empty projects and the empty everything else group', async () => {
      const wrapper = await mountBoard({}, projects)

      expect(isGroupBodyVisible(wrapper, 0)).toBe(false)
      expect(isGroupBodyVisible(wrapper, 1)).toBe(false)
      expect(isGroupBodyVisible(wrapper, 2)).toBe(false)
    })

    it('always shows the plain kanban columns when there are no projects', async () => {
      const wrapper = await mountBoard()

      expect(wrapper.findAll('.project-group').length).toBe(0)
      const style = wrapper.find('.kanban-column-group').attributes('style') ?? ''
      expect(style).not.toContain('display: none')
    })

    it('lets a manual toggle override the auto-collapsed state', async () => {
      const tasks = [
        { id: '1', agentType: 'human', status: 'to_do', projectId: 'project_1' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)

      const toggle = wrapper.findAll('.project-group').at(0)!.find('.project-group-toggle')
      expect(isGroupBodyVisible(wrapper, 0)).toBe(false)

      await toggle.trigger('click')
      expect(isGroupBodyVisible(wrapper, 0)).toBe(true)

      await toggle.trigger('click')
      expect(isGroupBodyVisible(wrapper, 0)).toBe(false)
    })

    it('does not auto-collapse an actionable project when search filters out its tasks', async () => {
      const tasks = [
        { id: '1', agentType: 'human', status: 'drafting', title: 'Alpha work', projectId: 'project_1' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)
      expect(isGroupBodyVisible(wrapper, 0)).toBe(true)

      vi.useFakeTimers()
      try {
        document.body.dispatchEvent(
          new KeyboardEvent('keydown', { key: 'f', metaKey: true, ctrlKey: true, bubbles: true })
        )
        await wrapper.vm.$nextTick()
        await wrapper.find('.search-input').setValue('no-match-zzz')
        vi.advanceTimersByTime(150)
        await wrapper.vm.$nextTick()

        const shownTasks = wrapper.findAll('.project-group').at(0)!
          .findAllComponents(VirtualTaskList)
          .flatMap((list: VueWrapper) => list.props().tasks as FullTask[])
        expect(shownTasks.length).toBe(0)
        expect(isGroupBodyVisible(wrapper, 0)).toBe(true)
      } finally {
        vi.useRealTimers()
      }
    })
  })

  describe('per-project archive', () => {
    const projects = [
      { id: 'project_1', workspaceId: 'test-workspace-id', title: 'Alpha', priority: 'high' },
    ] as Project[]

    const findArchiveButton = (wrapper: VueWrapper, groupIndex: number) =>
      wrapper.findAll('.project-group').at(groupIndex)!
        .findAll('.new-task.mini-button')
        .find(button => button.text() === '📦')!

    const archiveCalls = () =>
      (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls
        .filter(call => call[0].toString().includes('archive_finished'))

    beforeEach(() => {
      vi.stubGlobal('confirm', vi.fn(() => true))
    })

    it("archives a project's finished tasks via the project-scoped endpoint", async () => {
      const tasks = [
        { id: '1', agentType: 'none', status: 'complete', projectId: 'project_1' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)

      await findArchiveButton(wrapper, 0).trigger('click')
      await flushPromises()

      expect(archiveCalls().length).toBe(1)
      expect(archiveCalls()[0][0].toString()).toContain('/tasks/archive_finished?projectId=project_1')
    })

    it('archives unassigned finished tasks with an explicit empty projectId', async () => {
      const tasks = [
        { id: '1', agentType: 'none', status: 'complete' },
      ] as FullTask[]
      const wrapper = await mountBoard({ tasks }, projects)

      await findArchiveButton(wrapper, 1).trigger('click')
      await flushPromises()

      expect(archiveCalls().length).toBe(1)
      expect(archiveCalls()[0][0].toString()).toMatch(/archive_finished\?projectId=$/)
    })
  })

  describe('drag and drop between projects', () => {
    const projects = [
      { id: 'project_1', workspaceId: 'test-workspace-id', title: 'Alpha', priority: 'high' },
      { id: 'project_2', workspaceId: 'test-workspace-id', title: 'Beta', priority: 'none' },
    ] as Project[]

    const tasks = [
      {
        id: '1',
        workspaceId: 'test-workspace-id',
        agentType: 'human',
        status: 'to_do',
        title: 'Task one',
        description: 'a description',
        flowType: 'basic_dev',
        flowOptions: { envType: 'local' },
        projectId: 'project_1',
        created: new Date(),
        updated: new Date(),
        flows: [],
      },
    ] as FullTask[]

    const dropTask = async (wrapper: VueWrapper, groupIndex: number, taskId: string) => {
      await wrapper.findAll('.project-group').at(groupIndex)!.trigger('drop', {
        dataTransfer: { getData: () => taskId },
      })
      await flushPromises()
    }

    const putCalls = () =>
      (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls
        .filter(call => call[1]?.method === 'PUT')

    it('marks kanban task lists as draggable', async () => {
      const wrapper = await mountBoard({ tasks }, projects)
      const lists = wrapper.findAllComponents(VirtualTaskList)
      expect(lists.length).toBeGreaterThan(0)
      lists.forEach(list => {
        expect(list.props().draggable).toBe(true)
      })
    })

    it('reassigns a task dropped on another project group, preserving status', async () => {
      const wrapper = await mountBoard({ tasks }, projects)

      await dropTask(wrapper, 1, '1')

      expect(putCalls().length).toBe(1)
      const [url, options] = putCalls()[0]
      expect(url.toString()).toContain('/tasks/1')
      const body = JSON.parse(options.body as string)
      expect(body.projectId).toBe('project_2')
      expect(body.status).toBe('to_do')
      expect(body.agentType).toBe('human')
      expect(body.flowType).toBe('basic_dev')
      expect(body.title).toBe('Task one')
    })

    it('clears the project assignment when dropped on the everything else group', async () => {
      const wrapper = await mountBoard({ tasks }, projects)

      await dropTask(wrapper, 2, '1')

      expect(putCalls().length).toBe(1)
      const body = JSON.parse(putCalls()[0][1].body as string)
      expect(body.projectId).toBe('')
    })

    it('does nothing when a task is dropped on its own project group', async () => {
      const wrapper = await mountBoard({ tasks }, projects)

      await dropTask(wrapper, 0, '1')

      expect(putCalls().length).toBe(0)
    })
  })
})