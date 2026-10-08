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
      - frontend/src/components/KanbanColumnGroup.vue
      - frontend/src/lib/kanbanNavigation.ts
      - frontend/src/lib/pageNavigation.ts
      - frontend/src/lib/taskNavigation.ts:openTaskFlow
      - frontend/src/components/VirtualTaskList.vue
      - frontend/src/assets/keyboardNavigation.css
      - frontend/src/App.vue
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

- Focus is the highlight: there is no separate highlight state, so a card can
  never look highlighted while anything else holds focus.
- Keyboard-driven focus is outlined with `2px solid` primary color, leaving
  borders unchanged, on every navigable item: task cards, sidebar links, the
  workspace selector and its settings link, the column-heading `+` buttons,
  project group toggles and archive buttons, and the Draft/Queue Task buttons.
  It replaces the browser's own focus ring, and hover-revealed buttons stay
  visible while keyboard focused. Mouse-driven focus shows no outline; the
  next movement key moves on from the clicked element and is outlined. On
  load, the top-left card is focused and outlined.
- The remembered card is where focus goes when entering the board, when a key
  is pressed with nothing focused, and when a modal closes. Until the user
  steers it, it is the top-left card in the current display order, refocused
  as tasks change or stream in while a card holds focus. Focusing a card
  remembers it, and a remembered card that is no longer visible (e.g. its
  group collapsed) falls back to the top-left card. Focus is never taken from
  a non-card element.
- Once a task modal creates, starts or deletes a task, the remembered card
  returns to the top-left card, as if the page were reloaded. Canceling out
  of a task's modal remembers that task.
- Task cards are focusable only programmatically: Tab skips them and follows
  the regular tab order.
- Revealing a card scrolls it clear of the fixed and sticky headers above it.

### Navigation

Arrow keys and the secondary keys behave identically, whenever nothing has
focus or a navigable item does. They are ignored in text fields, with
modifiers, with an open overlay, and with a task modal open.

- Sidebar links: up/down move between links, clamped. Right enters the board.
- Header (workspace selector, settings link): left/right move between items;
  left from the first moves to the sidebar's current link. Down enters the
  board. While its overlay is closed, the workspace selector leaves movement
  keys to navigation, opening only via Enter, Space or click.
- Entering the board, only on the board page, focuses the remembered card if
  visible, else the top-left card, else the first board item.
- The board is laid out as rows, top to bottom: the column-heading `+`
  buttons, then per group its header (toggle, archive button), and when
  expanded its cards and its Draft/Queue Task buttons. Collapsed groups have
  only their header; groups without a rendered header have none.
- Up/down keep the column, moving within a column's cards and on to the
  nearest row with an item in that column. Group headers span all columns,
  so moving on from one continues in the column the user came from.
- Left/right move to the nearest item in the same row, keeping the card
  position where possible, clamped.
- Left from the leftmost item of a board row moves to the sidebar's current
  link; up from the column-heading row moves to the header. Other moves past
  the edges do nothing.
- Enter opens a focused task, as clicking it does. Other elements keep their
  native activation, and focus stays on a group toggle after toggling it.

## Returning after submission

- When enabled, submitting a user request with Cmd/Ctrl+Enter, leaving no other
  pending user request in that flow, moves on:
  - From a flow page, back to the board.
  - From a sub-task panel in the intent canvas, to the next sub-task needing
    attention (a pending clarification or blocked), or closes the panel if
    none.
- Cmd/Ctrl+Shift+Enter submits without moving on. Mouse submissions never move
  on.