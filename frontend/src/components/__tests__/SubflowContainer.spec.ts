import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import FlowActionItem from '../FlowActionItem.vue'
import SubflowContainer from '../SubflowContainer.vue'
import type { FlowAction, SubflowTree } from '../../lib/models'

describe('SubflowContainer', () => {
  const mockDate = new Date('2023-01-01T00:00:00.000Z');

  beforeEach(() => {
    window.HTMLElement.prototype.scrollIntoView = vi.fn()
  })

  it('renders correctly for a SubflowTree with only FlowActions', async () => {
    const subflowTree: SubflowTree = {
      name: 'root',
      children: [
        { 
          subflow: 'root', 
          id: '1', 
          flowId: '1', 
          workspaceId: '1', 
          created: mockDate,
          updated: mockDate,
          actionType: 'someType',
          actionParams: {},
          actionStatus: 'complete',
          actionResult: 'someResult',
          isHumanAction: false,
        },
        { 
          subflow: 'root', 
          id: '2', 
          flowId: '1', 
          workspaceId: '1', 
          created: mockDate,
          updated: mockDate,
          actionType: 'someType2',
          actionParams: {},
          actionStatus: 'complete',
          actionResult: '{"key": "someResult2"}',
          isHumanAction: false,
        },
        { 
          subflow: 'root', 
          id: '3', 
          flowId: '1', 
          workspaceId: '1', 
          created: mockDate,
          updated: mockDate,
          actionType: 'someType3',
          actionParams: {},
          actionStatus: 'complete',
          actionResult: '{"key": "someResult3"}',
          isHumanAction: false,
        },
      ]
    }

    const wrapper = mount(SubflowContainer, {
      props: { subflowTree, defaultExpanded: true }
    })

    expect(wrapper.find('.subflow-name').text()).toBe('root')
    const flowActionItems = wrapper.findAllComponents(FlowActionItem)
    expect(flowActionItems.length).toBe(3)
    expect(flowActionItems[0].props().flowAction).toStrictEqual(subflowTree.children[0])
    expect(flowActionItems[1].props().flowAction).toStrictEqual(subflowTree.children[1])
  })

  it('updates rendered child content when flow action data changes', async () => {
    const flowAction: FlowAction = {
      subflow: 'root',
      id: '1',
      flowId: '1',
      workspaceId: '1',
      created: mockDate,
      updated: mockDate,
      actionType: 'someType',
      actionParams: { param1: 'value1' },
      actionStatus: 'started',
      actionResult: '',
      isHumanAction: false,
    }
    const subflowTree: SubflowTree = {
      name: 'root',
      children: [flowAction]
    }

    const wrapper = mount(SubflowContainer, {
      props: { subflowTree, defaultExpanded: true }
    })

    // Verify initial rendered content shows "started" indicator
    expect(wrapper.text()).toContain('...')
    // The FlowActionItem should be expanded (last child with defaultExpanded=true)
    expect(wrapper.findComponent(FlowActionItem).classes()).toContain('expanded-action')
    // Verify params are rendered in the DOM
    expect(wrapper.text()).toContain('param1')

    // Simulate real-time update: action completes with a result
    const updatedFlowAction: FlowAction = {
      ...flowAction,
      actionStatus: 'complete',
      actionResult: '{"Response": "execution finished"}',
      updated: new Date('2023-01-01T00:01:00.000Z'),
    }
    await wrapper.setProps({
      subflowTree: {
        name: 'root',
        children: [updatedFlowAction]
      }
    })
    await nextTick()

    // Verify rendered DOM content actually changed
    expect(wrapper.text()).not.toContain('...')
    expect(wrapper.text()).toContain('execution finished')
    // Verify the FlowActionItem is still expanded
    expect(wrapper.findComponent(FlowActionItem).classes()).toContain('expanded-action')
  })

  it('maintains manually toggled expanded state and updates rendered content after child changes', async () => {
    const makeFlowAction = (id: string, status: string = 'started', result: string = ''): FlowAction => ({
      subflow: 'root',
      id,
      flowId: '1',
      workspaceId: '1',
      created: mockDate,
      updated: mockDate,
      actionType: 'someType',
      actionParams: {},
      actionStatus: status as any,
      actionResult: result,
      isHumanAction: false,
    })

    // Use 3+ children to exceed autoExpandThreshold so it starts collapsed
    const subflowTree: SubflowTree = {
      name: 'root',
      children: [
        makeFlowAction('1', 'complete', '{"key": "r1"}'),
        makeFlowAction('2', 'complete', '{"key": "r2"}'),
        makeFlowAction('3', 'started'),
      ]
    }

    const wrapper = mount(SubflowContainer, {
      props: { subflowTree, defaultExpanded: false }
    })

    // Should start collapsed — children not rendered
    expect(wrapper.find('.subflow-name').classes()).not.toContain('name-expanded')
    expect(wrapper.findAllComponents(FlowActionItem).length).toBe(0)

    // Manually expand by clicking the heading
    await wrapper.find('.subflow-name-container').trigger('click')
    await nextTick()
    expect(wrapper.find('.subflow-name').classes()).toContain('name-expanded')
    expect(wrapper.findAllComponents(FlowActionItem).length).toBe(3)

    // Third child is "started", verify "..." indicator is rendered
    expect(wrapper.text()).toContain('...')

    // Update: third child completes with new result
    await wrapper.setProps({
      subflowTree: {
        name: 'root',
        children: [
          makeFlowAction('1', 'complete', '{"key": "r1"}'),
          makeFlowAction('2', 'complete', '{"key": "r2"}'),
          makeFlowAction('3', 'complete', '{"Response": "final output"}'),
        ]
      },
      defaultExpanded: false,
    })
    await nextTick()

    // Subflow should still be expanded because user manually toggled it
    expect(wrapper.find('.subflow-name').classes()).toContain('name-expanded')
    // All children should still be rendered
    const flowActionItems = wrapper.findAllComponents(FlowActionItem)
    expect(flowActionItems.length).toBe(3)
    // Rendered content should reflect the update
    expect(wrapper.text()).not.toContain('...')
  })

  it('renders correctly for a SubflowTree with nested SubflowTrees', async () => {
    const flowAction: FlowAction = { 
      subflow: 'root:|:nested', 
      id: '1', 
      flowId: '1', 
      workspaceId: '1', 
      created: mockDate,
      updated: mockDate,
      actionType: 'someOtherType',
      actionParams: {},
      actionStatus: 'complete',
      actionResult: JSON.stringify({ Response: 'someOtherResult' }),
      isHumanAction: false
    }
    const subflowTree: SubflowTree = {
      name: 'root',
      children: [
        {
          name: 'root:|:nested', 
          children: [
            flowAction
          ]
        }
      ]
    }

    const wrapper = mount(SubflowContainer, {
      props: { subflowTree, defaultExpanded: false }
    })

    expect(wrapper.find('.subflow-name').text()).toBe('root')
    const flowActionItems = wrapper.findAllComponents(FlowActionItem)
    expect(flowActionItems[0].props().flowAction).toStrictEqual(flowAction)
    const nestedSubflowContainers = wrapper.findAllComponents(SubflowContainer)
    expect(nestedSubflowContainers[0].props().subflowTree).toStrictEqual(subflowTree.children[0])
  })
})