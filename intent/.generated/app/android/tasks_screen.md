---
intent_links:
  - intent: "#landing-on-tasks"
    code:
      - app/android/app/src/main/kotlin/com/example/app/MainActivity.kt
      - app/android/app/src/main/kotlin/com/example/app/feature/pairing/PairingViewModel.kt:PairingViewModel
      - app/android/app/src/main/kotlin/com/example/app/feature/tasks/TasksViewModel.kt:TasksViewModel
      - app/android/app/src/main/kotlin/com/example/app/core/remote/WorkspaceSelectionStore.kt:WorkspaceSelectionStore
      - app/android/app/src/test/kotlin/com/example/app/TasksViewModelTest.kt
  - intent: "#workspace-switcher"
    code:
      - app/android/app/src/main/kotlin/com/example/app/feature/tasks/WorkspaceSwitcher.kt
      - app/android/app/src/test/kotlin/com/example/app/WorkspaceSwitcherJvmTest.kt
  - intent: "#task-ordering"
    code:
      - app/android/app/src/main/kotlin/com/example/app/feature/tasks/TaskRanking.kt:rankTasks
      - app/android/app/src/main/kotlin/com/example/app/feature/tasks/TaskRanking.kt:bucketTasks
      - app/android/app/src/test/kotlin/com/example/app/feature/tasks/TaskRankingTest.kt
  - intent: "#task-list"
    code:
      - app/android/app/src/main/kotlin/com/example/app/feature/tasks/TasksScreen.kt:TasksScreen
      - app/android/app/src/main/kotlin/com/example/app/core/ui/TaskStatusUi.kt
      - app/android/app/src/main/kotlin/com/example/app/core/ui/RelativeTime.kt
      - app/android/app/src/test/kotlin/com/example/app/TasksScreenJvmTest.kt
  - intent: "#search"
    code:
      - app/android/app/src/main/kotlin/com/example/app/feature/tasks/TaskRanking.kt:searchTasks
      - app/android/app/src/main/kotlin/com/example/app/feature/tasks/TasksScreen.kt:TasksScreen
  - intent: "#placeholders"
    code:
      - app/android/app/src/main/kotlin/com/example/app/feature/taskdetail/TaskDetailScreen.kt
      - app/android/app/src/test/kotlin/com/example/app/TaskDetailScreenJvmTest.kt
  - intent: "#visual-style"
    code:
      - app/android/app/src/main/kotlin/com/example/app/core/ui/theme/Color.kt
      - app/android/app/src/main/kotlin/com/example/app/core/ui/theme/Theme.kt:AppTheme
      - app/android/app/src/main/kotlin/com/example/app/core/ui/theme/Type.kt
      - intent/.generated/app/android/tasks_screen_wireframe.html
---
# Android Tasks Screen

> Generated/inferred intent. Trusted less than human-authored intent; it records
> consequential, high-level inferences only and is not the source of truth.

## Problem

The Android companion app pairs with a Sidekick server but then offers only a
workspace list and a sparse, card-per-task list with no ordering, search or
task detail. It should land on the work that matters, fit several tasks on a
phone screen, and use the Sidekick brand rather than the stock Material palette,
while staying simple given the app's limited functionality.

The approved layout is captured in [the wireframe](tasks_screen_wireframe.html);
its defaults are the approved choices and its other toggle values exist only
for comparison.

## Landing on tasks

After pairing, or on launch with stored credentials, the app opens the Tasks
screen directly. The workspace shown is, in order of preference: the last
workspace the user selected on this device, the workspace hinted by the pairing
QR payload, then the first workspace the server returns. Each candidate is used
only if the server still lists it. The chosen workspace is persisted locally.

The Pairing screen is shown only when the device is not paired or stored
credentials cannot be read; it offers a single "Scan pairing code" action under
the heading "Connect to Sidekick". There is no standalone workspace list.

## Workspace switcher

The Tasks top bar shows the current workspace name with a trailing,
vertically centred chevron. While workspaces are still loading the title area
stays populated so the bar never blanks. Tapping the title opens a full-screen
workspace picker with a close action, a filter field (case-insensitive
substring on name), one row per workspace with the current one marked, and a
"Scan a different pairing code" entry at the bottom. The same scan entry is
also available from the top bar overflow menu.

Selecting a workspace closes the picker, persists the selection and reloads
tasks in place; the top bar keeps showing the newly selected name during the
reload. The picker has loading, error-with-retry and empty states.

## Task ordering

Tasks are grouped into fixed buckets and ordered by bucket, then by `updated`
descending within a bucket:

1. Needs attention: `blocked`, `in_review`
2. Active: `in_progress`, `to_do`
3. Drafts: `drafting`
4. Done: `complete`, `failed`, `canceled`

Unknown statuses fall to the end of the Active bucket. Unparsable `updated`
values sort last within their bucket. Bucketing and ranking are pure functions
independent of Compose and are unit tested.

## Task list

Below the top bar, a single-select row of filter chips shows `Open`, `Drafts`
and `Done`, each with its count. `Open` is selected by default and lists the
Needs attention bucket followed by the Active bucket with no section headers;
the ordering and status indicators carry the grouping. `Drafts` and `Done`
show their bucket alone, so finished tasks are hidden by default and reachable
in one tap. There are no separate collapsed rows at the bottom of the list.

Rows are compact and flat (no elevated cards), separated by hairline dividers,
with horizontal padding of at most 16dp: title up to two lines, relative
updated time ("just now", "5m ago", "3h ago", "2d ago") right-aligned on the
first line, and on the second line a small status indicator followed by an
optional one-line description preview. The status indicator is a coloured dot
plus a human-readable label ("In review", "To do", "In progress", "Blocked",
"Draft", "Complete", "Failed", "Canceled"; unknown statuses are humanised).
At least five rows are fully visible on a 360×640dp screen with the top bar
and chip row shown.

The list supports pull-to-refresh and retains loading, error-with-retry and
empty states.

## Search

A search icon in the top bar swaps the bar for a search field with back and
clear actions. Filtering is client-side, case-insensitive substring match on
title and description across all loaded tasks including Drafts and Done. While
a query is active the results are one flat list ranked by the same bucket
order then `updated` descending, so Drafts and Done are down-ranked rather than
hidden; a short note reports the result count. No results shows a short
message. Closing or clearing search restores the chip-filtered view, and the
list keeps its scroll position where reasonable.

## Placeholders

Tapping a row opens a Task Detail screen showing the title, status indicator,
full description and a clear "Coming soon" note, with Edit, Cancel and Archive
actions that show a transient "Not available yet" message. Back returns to the
list preserving its scroll and search state. The Tasks top bar has a "+" new
task action that shows the same "Not available yet" message. Layouts leave room
for these actions to become real later.

## Visual style

The theme is neutral/monochrome (near-white and near-black surfaces, grey
text tiers, hairline dividers) with a single accent: the Sidekick brand purple
used by the web CTA (`rgb(131, 58, 180)`), lifted to a lighter tint in dark
mode so text on it stays legible. Status colours are limited to the dot and
label. Dark mode is fully supported via the system setting, and typography is
tuned for density (smaller title, body and label sizes than the Material 3
defaults).

## Constraints

- Pairing, credential storage and the remote API contract are unchanged; the
  server returns all non-archived tasks and bucketing is client-side.
- The existing JVM/Robolectric test approach is kept and extended; instrumented
  launch behaviour when unpaired (heading "Connect to Sidekick", "Scan pairing
  code" action) is preserved.
- New dependencies are kept minimal and pinned in the Gradle version catalog.
- Out of scope: real task detail, creating/editing/cancelling/archiving tasks,
  server-side search, live updates and pagination of archived tasks.