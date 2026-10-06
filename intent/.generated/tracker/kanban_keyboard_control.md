---
intent_links:
  - intent: "#local-settings"
    code:
      - frontend/src/lib/localSettings.ts
      - frontend/src/views/LocalSettingsView.vue
      - frontend/src/router/index.ts
      - frontend/src/App.vue
  - intent: "#board-highlight"
    code:
      - frontend/src/components/KanbanBoard.vue:handleKeyboardControl
      - frontend/src/lib/kanbanNavigation.ts
      - frontend/src/lib/taskNavigation.ts:openTaskFlow
      - frontend/src/components/TaskCard.vue
      - frontend/src/components/VirtualTaskList.vue
  - intent: "#returning-after-submission"
    code:
      - frontend/src/components/UserRequest.vue:submitViaShortcut
      - frontend/src/lib/globalShortcutRegistry.ts
      - frontend/src/views/FlowView.vue:handleUserRequestShortcutSubmitted
      - frontend/src/views/IntentCanvasView.vue:openNextSubtaskNeedingAttention
---

# Kanban keyboard control

## Problem

Working through tasks needing attention requires the mouse: picking a card on
the board, then returning to the board after responding to a flow.

## Local settings

- A dev-mode-only "Local Settings" page holds client-local preferences, stored
  in the browser and applied across all workspaces. Outside dev mode the page
  is unavailable and stored values are ignored.
- "Keyboard control for kanban board" is off by default. When off, the UI is
  unchanged.
- Secondary movement keys default to vim-style `h`/`j`/`k`/`l` and are
  user-configurable; each is a single character, all four are distinct, and
  none shadows an existing board shortcut. Arrow keys always work too.
- Changes take effect immediately.

## Board highlight

- When enabled, exactly one visible task card is highlighted, styled like a
  focused text area.
- Until the user moves it, the highlight is the top-left card in the current
  display order, including as tasks change or stream in. Afterwards it follows
  the chosen task, falling back to the top-left card once that task is no
  longer visible.
- Up/down move within a column and continue into the same column of adjacent
  expanded groups. Left/right move to the nearest non-empty column in the same
  group at the same position, clamped. Moves past the edges do nothing.
- Enter opens the highlighted task, as clicking it does.
- Board keys are ignored while typing in an input or with a task modal open.

## Returning after submission

- When enabled, submitting a user request with Cmd/Ctrl+Enter, leaving no other
  pending user request in that flow, moves on:
  - From a flow page, back to the board.
  - From a sub-task panel in the intent canvas, to the next sub-task needing
    attention (a pending clarification or blocked), or closes the panel if
    none.
- Cmd/Ctrl+Shift+Enter submits without moving on. Mouse submissions never move
  on.