import type { FlowAction, SubflowTree } from "./models";

/*
Given a list of flow actions like this:

{subflow: 'a', actionType: 'action1', ...},
{subflow: 'a:|:x', actionType: 'action2', ...},
{subflow: 'a:|:y', actionType: 'action3', ...},
{subflow: 'a:|:x', actionType: 'action2b', ...},
{subflow: 'b:|:z', actionType: 'action4', ...},
{subflow: 'b', actionType: 'action5', ...},

Create an array subflow tree like this:

[
  {
    name: 'a',
    children: [
      { actionType: 'action1', ... },
      {
        name: 'x',
        children: [
          { actionType: 'action2', ... },
        ],
      },
      {
        name: 'y',
        children: [
          { actionType: 'action3', ... },
        ],
      },
      {
        name: 'x',
        children: [
          { actionType: 'action2b', ... },
        ],
      },
    ],
  },
  {
    name: 'b',
    children: [
      {
        name: 'z',
        children: [
          { actionType: 'action4', ... },
        ],
      },
      { actionType: 'action5', ... },
    ],
  },
]
*/
export const buildSubflowTrees = (flowActions: FlowAction[]): SubflowTree[] => {
  const subflowTrees: SubflowTree[] = [];
  let ancestors: SubflowTree[] = [];
  const subflowDescriptions: { [subflow: string]: string } = {};
  const subflowIds: { [subflow: string]: string } = {};

  for (const action of flowActions) {
    const subflows = action.subflow.split(':|:');

    if (action.subflowDescription && action.subflowDescription.length > 0) {
      const lastSubflow = subflows[subflows.length - 1]
      subflowDescriptions[lastSubflow] = action.subflowDescription;
    }

    if (action.subflowId) {
      const lastSubflow = subflows[subflows.length - 1]
      subflowIds[lastSubflow] = action.subflowId;
    }

    let parent: SubflowTree | undefined;
    let i = 0;

    // Find the longest common prefix with the ancestors
    while (i < ancestors.length && i < subflows.length && ancestors[i].name === subflows[i]) {
      parent = ancestors[i];
      i++;
    }

    // Remove the extra ancestors
    ancestors = ancestors.slice(0, i);

    // Add the new nodes
    for (; i < subflows.length; i++) {
      const newSubflowTree: SubflowTree = { name: subflows[i], children: [] };
      const description = subflowDescriptions[newSubflowTree.name]
      //console.log({name: newSubflowTree.name, description})
      if (description && description.length > 0) {
        newSubflowTree.description = description;
      }
      const id = subflowIds[newSubflowTree.name];
      if (id) {
        newSubflowTree.id = id;
      }

      (parent ? parent.children : subflowTrees).push(newSubflowTree);
      ancestors.push(newSubflowTree);
      parent = newSubflowTree;
    }

    // Add the action to the last node
    parent!.children.push(action);
  }
  //console.log(subflowDescriptions);
  //console.log(subflowTrees);

  return subflowTrees;
};