import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import FlowActionItem from '../FlowActionItem.vue'
import SubflowContainer from '../SubflowContainer.vue'
import type { FlowAction, SubflowTree } from '../../lib/models'

describe('SubflowContainer', () => {
  const mockDate = new Date('2023-01-01T00:00:00.000Z');

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