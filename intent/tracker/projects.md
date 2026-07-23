---
intent_links:
  - intent: "#api"
    code:
      - domain/project.go
      - srv/sqlite/project.go
      - srv/sqlite/project_test.go
      - srv/sqlite/migrations/014_create_projects_table.up.sql
      - srv/sqlite/migrations/014_create_projects_table.down.sql
      - srv/redis/project.go
      - srv/redis/project_test.go
      - srv/service.go:Storage
      - srv/delegator.go
      - api/project_api.go
      - api/project_api_test.go
      - api/api.go:DefineRoutes
---
# Projects

## Domain Model

Fields:

- id (ksuid)
- workspace id (required)
- title (required)
- description (optional)
- priority: no priority, low, medium, high, urgent

## Storage

Redis and sqlite implementations exist to support persistence.

## API

REST endpoints exist to create, update, list, get and delete projects.

## Ordering

While projects have a priority, within each priority bucket, projects have
relative priority ordering.

## Frontend

### Create/Edit Project Page

There is a dedicated page for creating/editing a project (same component for
both).

### Projects List Page

The projects list page allows using drag and drop to order projects. allows
creating, editing and deleting projects.

The main left sidebar has a projects tab to access this page.