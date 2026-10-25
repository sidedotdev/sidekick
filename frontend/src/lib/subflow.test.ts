import { describe, it, expect } from 'vitest'
import { buildSubflowTrees } from './subflow';
import type { SubflowTree, FlowAction } from './models';

describe('buildSubflowTrees', () => {
  it('returns no trees without actions or subflows', () => {
    expect(buildSubflowTrees([], {})).toEqual([])
  })

  it('orders actions by creation time regardless of REST/websocket arrival order', () => {
    const newest = {
      id: 'newest', actionType: 'test', created: new Date('2026-01-01T00:00:02Z'),
    } as FlowAction
    const oldest = {
      id: 'oldest', actionType: 'test', created: new Date('2026-01-01T00:00:01Z'),
    } as FlowAction
    const expected: SubflowTree[] = [{
      id: 'ungrouped-actions', name: 'Actions', children: [oldest, newest],
    }]
    expect(buildSubflowTrees([newest, oldest], {})).toEqual(expected)
    expect(buildSubflowTrees([oldest, newest], {})).toEqual(expected)
  })
})