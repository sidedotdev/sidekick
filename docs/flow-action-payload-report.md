# Flow action payloads

Measured `flow_3KFEV6chTRqgtk6lO9vHle76tvx`: 991 captured actions, imported into fresh SQLite. Temporary Go test: one warm-up, 15 rotating trials, isolated run. Medians below include SQL/decoding, JSON encoding, zstd and Go response parsing—not network or browser rendering.

| Payload | JSON MB | zstd MB | Pipeline ms |
|---|---:|---:|---:|
| Full | 15.82 | 1.205 | 405 |
| Without params | 5.67 | 1.094 | 108 |
| Without results | 11.05 | 0.224 | 330 |
| Without both | 0.90 | 0.080 | 29 |
| Newest 50, full payloads | 0.74 | 0.094 | 21 |

Params dominate processing; results dominate compressed transfer. Full SQL/decoding took 123 ms, JSON encoding 134 ms, compression 21 ms. Live endpoint transfers were 16.02 MB identity / 1.24 MB zstd.

## Payload sources and initial display

All visible rows show type/status; collapsed subflows do not mount their actions.

| Action types | Size source; collapsed row / expanded body |
|---|---|
| `generate.code_edits`, `generate.context_gather` | Code edits alone: params 9.63 MB (`messages` 5.00, `tools` 4.55), results 1.48 MB. Collapsed: no payload display. Expanded: model metadata and response; message history remains hidden behind “Show”. |
| `tool_call.run_command` | Results 2.26 MB. Collapsed: params `command`, `workingDir`, result `exitStatus`; expanded: output. |
| `tool_call.get_symbol_definitions`, `bulk_search_repository`, `read_file_lines` | Results 0.50/0.19/0.003 MB. Collapsed: request paths/symbol names, search terms/globs, or file/line ranges from params; expanded: returned content. |
| `apply_edit_blocks` | Params/results 0.11/0.29 MB. Collapsed: result `didApply` and `originalEditBlocks` counts; expanded: edit reports. |
| `user_request`, `.paused`, `.continue`, `.approve.run_command` | Command approval params 0.19 MB, chiefly permission evaluation. Pending: request text/controls from params. Completed approvals collapse to result `Approved`; other human requests default expanded with request/response content. Completed continue requests are hidden. |
| `ranked_repo_summary`, `tool_call.set_base_branch` | Under 2 KB combined. Collapsed: no payload display; expanded: summary text or tool params/result. |