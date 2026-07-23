---
intent_links:
  - intent: "#domain-model"
    code:
      - domain/task.go:Task
      - srv/sqlite/task.go
      - srv/sqlite/task_test.go:TestPersistTask
      - srv/sqlite/migrations/015_add_project_id_to_tasks.up.sql
      - srv/sqlite/migrations/015_add_project_id_to_tasks.down.sql
      - api/api.go:TaskRequest
      - api/api.go:CreateTaskHandler
      - api/api.go:UpdateTaskHandler
      - api/api_test.go:TestCreateTaskHandler_ProjectId
      - api/api_test.go:TestUpdateTaskHandler_ProjectIdPresenceSemantics
      - frontend/src/lib/models.ts:Task
      - frontend/src/lib/models.ts:Project
  - intent: "#project"
    code:
      - frontend/src/components/TaskModal.vue
      - frontend/src/components/__tests__/TaskModal.spec.ts
      - frontend/src/components/TaskCard.vue:copiedTask
      - frontend/src/components/KanbanBoard.vue
      - frontend/src/components/KanbanColumnGroup.vue
      - frontend/src/components/VirtualTaskList.vue
      - frontend/src/components/__tests__/KanbanBoard.spec.ts
---
# Tasks

Note: actual tasks code predates this intent file. Thus this intent file is
known to be incomplete.

## Domain Model

Incomplete Fields:

- id (ksuid)
- workspace id (required)
- title
- description
- project id (optional)

## Frontend

### Task Modal

#### Project

When creating or editing tasks, projects can be assigned via a pill-style
dropdown. existing tasks can be dragged and dropped between projects, though
they keep their status as-is (no drag and drop between columns/statuses is
allowed).