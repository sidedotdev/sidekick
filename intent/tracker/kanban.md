---
intent_links:
  - intent: "#projects"
    code:
      - api/api.go:ArchiveFinishedTasksHandler
      - api/api_test.go:TestArchiveFinishedTasksHandler_ProjectFilter
      - frontend/src/components/KanbanBoard.vue
      - frontend/src/components/KanbanColumnGroup.vue
      - frontend/src/components/__tests__/KanbanBoard.spec.ts
---
# Kanban

Note: actual kanban code predates this intent file. Thus this intent file is
known to be incomplete.

## Frontend

### Projects

The kanban board groups tasks by project, as long as there is at least one project.

Projects can be collapsed/expanded to hide/show tasks.

Projects are displayed in the kanban board in priority/rank order, and show
above the "everything else" group in the kanban board.

Existing tasks can be dragged and dropped between projects, though they keep
their status as-is (no drag and drop between columns/statuses is allowed).

When none of the tasks in a project are actionable (actionable means status is
drafting/blocked/in_review), the project is auto-collapsed.

There is a way to add a task in a specific project, reusing the existing buttons
within kanban columns, which auto-selects the right project by default.

A single project's finished tasks can be archived with a single click without
archiving other tasks. This is similar to the existing "archive all"
functionality, but project-limited.