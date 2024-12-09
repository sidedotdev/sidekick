import { describe, it, expect, vi } from 'vitest'
import { buildSubflowTrees } from './subflow';
import type { SubflowTree, FlowAction } from './models';

describe('buildSubflowTrees', () => {
  it('should return an empty array when flowActions is empty', () => {
    const flowActions: FlowAction[] = [];
    const result = buildSubflowTrees(flowActions);
    expect(result).toEqual([]);
  });

  it('should return an array with a single SubflowTree when flowActions contains a single FlowAction with a single-level subflow', () => {
    const flowAction: FlowAction = { subflow: 'a', actionType: 'action1' } as FlowAction;
    const flowActions: FlowAction[] = [flowAction];
    const result = buildSubflowTrees(flowActions);
    expect(result).toEqual([{ name: 'a', children: [flowAction] }]);
  });

  it('should return an array with a single SubflowTree when flowActions contains multiple FlowActions with the same single-level subflow', () => {
    const flowAction1: FlowAction = { subflow: 'a', actionType: 'action1' } as FlowAction;
    const flowAction2: FlowAction = { subflow: 'a', actionType: 'action2' } as FlowAction;
    const flowActions: FlowAction[] = [flowAction1, flowAction2];
    const result = buildSubflowTrees(flowActions);
    expect(result).toEqual([{ name: 'a', children: [flowAction1, flowAction2] }]);
  });

  it('should return an array with SubflowTrees that correctly represent the hierarchy of the subflows when flowActions contains FlowActions with multi-level subflows', () => {
    const flowAction1: FlowAction = { subflow: 'a', actionType: 'action1' } as FlowAction;
    const flowAction2: FlowAction = { subflow: 'a:|:x', actionType: 'action2' } as FlowAction;
    const flowActions: FlowAction[] = [flowAction1, flowAction2];
    const result = buildSubflowTrees(flowActions);
    expect(result).toEqual([
      { 
        name: 'a', 
        children: [
          flowAction1,
          { name: 'x', children: [flowAction2] }
        ] 
      }
    ]);
  });

  it('should return an array with multiple SubflowTrees when flowActions contains FlowActions with different subflows', () => {
    const flowAction1: FlowAction = { subflow: 'a', actionType: 'action1' } as FlowAction;
    const flowAction2: FlowAction = { subflow: 'b', actionType: 'action2' } as FlowAction;
    const flowActions: FlowAction[] = [flowAction1, flowAction2];
    const result = buildSubflowTrees(flowActions);
    expect(result).toEqual([
      { name: 'a', children: [flowAction1] },
      { name: 'b', children: [flowAction2] },
    ]);
  });
  it('should group actions with the same subflow under the same SubflowTree', () => {
    const flowActions: FlowAction[] = [
      { subflow: 'a', actionType: 'action1' } as FlowAction,
      { subflow: 'a', actionType: 'action2' } as FlowAction,
    ];
    const result = buildSubflowTrees(flowActions);
    expect(result).toEqual([
      {
        name: 'a',
        children: [
          flowActions[0],
          flowActions[1],
        ],
      },
    ]);
  });

  it('should make SubflowTrees for longer subflows children of SubflowTrees for shorter subflows', () => {
    const flowActions: FlowAction[] = [
      { subflow: 'a', actionType: 'action1' } as FlowAction,
      { subflow: 'a:|:x', actionType: 'action2' } as FlowAction,
    ];
    const result = buildSubflowTrees(flowActions);
    expect(result).toEqual([
      {
        name: 'a',
        children: [
          flowActions[0],
          { name: 'x', children: [ flowActions[1] ] },
        ],
      },
    ]);
  });
  it('should build subflow trees correctly for given flow actions', () => {
    const flowActions: FlowAction[] = [
      {subflow: 'a', actionType: 'action1'},
      {subflow: 'a:|:x', actionType: 'action2'},
      {subflow: 'a:|:y', actionType: 'action3'},
      {subflow: 'a:|:x', actionType: 'action2b'},
      {subflow: 'b:|:z', actionType: 'action4'},
      {subflow: 'b', actionType: 'action5'},
    ] as FlowAction[];

    const expectedSubflowTrees: SubflowTree[] = [
      {
        name: 'a',
        children: [
          flowActions[0],
          { name: 'x', children: [ flowActions[1] ] },
          { name: 'y', children: [ flowActions[2] ] },
          { name: 'x', children: [ flowActions[3] ] },
        ],
      },
      {
        name: 'b',
        children: [
          { name: 'z', children: [ flowActions[4] ] },
          flowActions[5],
        ],
      },
    ];

    const result = buildSubflowTrees(flowActions);
    expect(result).toEqual(expectedSubflowTrees);
  });
});