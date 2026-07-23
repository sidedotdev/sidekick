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

REST endpoints exist to create, update and delete projects.

## Ordering

Phile projects have a priority within each priority bucket, we can adjust
relative rankings of projects for fine-grained prioritization,

## Frontend

The projects page allows using drag and drop to order projects, and allows
creating, editing and deleting proejcts.

The main left sidebar has a projects tab to access this page.

kanban board groups tasks by project. projects can be collapsed/expanded to
hide/show tasks. projects are displayed in the kanban board in order, and show
above "everything else" in the kanban board.

when creating or editing tasks, projects can be assigned via a pill-style
dropdown. existing tasks can be dragged and dropped between projects, though
they keep their status as-is (no drag and drop between columns/statuses is
allowed).

when none of the tasks in a project are actionable, the project is.
auto-collapsed. there is a way to add a task for a specific project, reusing the
existing buttons within kanban columns, which auto-selects the right project by
default.

a single projects finished tasks can be archived with a single click without
archiving other tasks. similar to the existing "archive all" functionality, but
project-limited.