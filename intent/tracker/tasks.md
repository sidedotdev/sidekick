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