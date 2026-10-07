import type {
  CommandPermissionEvaluation,
  PermissionFactor,
  PermissionResult,
  ScriptPermissionEvaluation,
} from './models'

export type ShellTokenKind =
  | 'command'
  | 'keyword'
  | 'option'
  | 'string'
  | 'variable'
  | 'operator'
  | 'comment'
  | 'heredoc'

export interface TextRange {
  start: number
  end: number
}

export interface ShellToken extends TextRange {
  kind: ShellTokenKind
}

interface ShellOperator extends TextRange {
  text: string
}

export interface ShellScan {
  tokens: ShellToken[]
  /** List/pipeline operators outside any subshell, substitution or quote. */
  topLevelOperators: ShellOperator[]
  heredocStarts: number[]
  /** Offsets where an executed simple command or compound command begins. */
  commandStarts: number[]
}

const OPERATORS = ['&&', '||', '|&', ';;', '&>>', '&>', '>>', '>&', '<<<', '<<-', '<<', '<>', '>|', '<&', '>', '<', '|', ';', '&', '(', ')']
const REDIRECTS = new Set(['&>>', '&>', '>>', '>&', '<<<', '<<-', '<<', '<>', '>|', '<&', '>', '<'])
const BREAKABLE_OPERATORS = new Set(['&&', '||', '|', '|&', ';'])
const KEYWORDS = new Set([
  'if', 'then', 'else', 'elif', 'fi', 'for', 'while', 'until', 'do', 'done',
  'case', 'esac', 'in', 'function', 'select', 'time', '!', '{', '}', '[[', ']]',
])
// Keywords after which the next word is again a command name.
const COMMAND_PREFIX_KEYWORDS = new Set(['if', 'then', 'else', 'elif', 'while', 'until', 'do', 'time', '!', '{'])
const WORD_TERMINATOR = /[\s;&|()<>'"`]/
const WORD_BOUNDARY = /[\s;&|()<>`]/
const ASSIGNMENT = /^[A-Za-z_][A-Za-z0-9_]*\+?=/
const SPECIAL_PARAMETER = /[0-9@*#?$!-]/

/**
 * Scans a bash script into highlight tokens. This is a display-oriented
 * approximation, not a full parser: unrecognized constructs fall back to
 * plain text rather than failing.
 */
export function scanShell(script: string): ShellScan {
  const n = script.length
  const tokens: ShellToken[] = []
  const topLevelOperators: ShellOperator[] = []
  const heredocStarts: number[] = []
  const commandStarts: number[] = []
  let pendingHeredocs: { delimiter: string; stripTabs: boolean }[] = []
  // expectCommand values to restore when the matching ")" closes.
  const groupStack: boolean[] = []
  let expectCommand = true
  let expectRedirectTarget = false
  let quotedSubstitutionDepth = 0

  const push = (start: number, end: number, kind: ShellTokenKind) => {
    if (end > start) tokens.push({ start, end, kind })
  }

  function isWordStart(at: number): boolean {
    return at === 0 || WORD_BOUNDARY.test(script.charAt(at - 1))
  }

  function variableEnd(at: number): number {
    const next = script.charAt(at + 1)
    if (next === '{') {
      const close = script.indexOf('}', at + 2)
      return close === -1 ? n : close + 1
    }
    const name = /[A-Za-z_][A-Za-z0-9_]*/y
    name.lastIndex = at + 1
    const match = name.exec(script)
    if (match) return at + 1 + match[0].length
    if (next !== '' && SPECIAL_PARAMETER.test(next)) return at + 2
    return at + 1
  }

  function isExpansionStart(at: number): boolean {
    return script.charAt(at) === '$' && (script.charAt(at + 1) === '(' || variableEnd(at) > at + 1)
  }

  // Scans the code of a `$(...)` nested in double quotes with fresh command
  // state, returning the offset just past its closing ")".
  function scanQuotedSubstitution(from: number): number {
    const saved = { expectCommand, expectRedirectTarget, groups: groupStack.splice(0) }
    expectCommand = true
    expectRedirectTarget = false
    quotedSubstitutionDepth++
    const end = scanCode(from)
    quotedSubstitutionDepth--
    expectCommand = saved.expectCommand
    expectRedirectTarget = saved.expectRedirectTarget
    groupStack.splice(0, groupStack.length, ...saved.groups)
    return end
  }

  function scanDoubleQuoted(from: number): number {
    let segmentStart = from
    let j = from + 1
    while (j < n) {
      const c = script.charAt(j)
      if (c === '\\') {
        j += 2
        continue
      }
      if (c === '"') {
        j++
        break
      }
      if (c === '$' && script.charAt(j + 1) === '(') {
        push(segmentStart, j, 'string')
        push(j, j + 2, 'operator')
        j = scanQuotedSubstitution(j + 2)
        segmentStart = j
        continue
      }
      if (c === '$' && variableEnd(j) > j + 1) {
        const end = variableEnd(j)
        push(segmentStart, j, 'string')
        push(j, end, 'variable')
        segmentStart = end
        j = end
        continue
      }
      j++
    }
    const end = Math.min(j, n)
    push(segmentStart, end, 'string')
    return end
  }

  function scanHeredocDelimiter(from: number, stripTabs: boolean): number {
    let j = from
    while (script.charAt(j) === ' ' || script.charAt(j) === '\t') j++
    const start = j
    while (j < n && !/[\s;&|()<>]/.test(script.charAt(j))) {
      const c = script.charAt(j)
      if (c === "'" || c === '"') {
        const close = script.indexOf(c, j + 1)
        j = close === -1 ? n : close + 1
      } else {
        j += c === '\\' ? 2 : 1
      }
    }
    j = Math.min(j, n)
    const delimiter = script.slice(start, j).replace(/['"\\]/g, '')
    if (delimiter) {
      push(start, j, 'string')
      pendingHeredocs.push({ delimiter, stripTabs })
    }
    return j
  }

  function consumeHeredocBodies(from: number): number {
    let pos = from
    pendingHeredocs.forEach(({ delimiter, stripTabs }, index) => {
      const bodyStart = pos
      let lineStart = pos
      for (;;) {
        const newline = script.indexOf('\n', lineStart)
        const lineEnd = newline === -1 ? n : newline
        const line = script.slice(lineStart, lineEnd)
        if ((stripTabs ? line.replace(/^\t+/, '') : line) === delimiter) {
          push(bodyStart, lineStart, 'heredoc')
          push(lineStart, lineEnd, 'string')
          pos = lineEnd
          break
        }
        if (lineEnd >= n) {
          push(bodyStart, n, 'heredoc')
          pos = n
          break
        }
        lineStart = lineEnd + 1
      }
      if (index < pendingHeredocs.length - 1 && pos < n) pos++
    })
    pendingHeredocs = []
    return pos
  }

  function matchOperator(at: number): ShellOperator | null {
    const fileDescriptor = /\d+(?=[<>])/y
    fileDescriptor.lastIndex = at
    const fdMatch = isWordStart(at) ? fileDescriptor.exec(script) : null
    const opStart = at + (fdMatch ? fdMatch[0].length : 0)
    const text = OPERATORS.find((op) => script.startsWith(op, opStart))
    if (!text || (fdMatch && !REDIRECTS.has(text))) return null
    let end = opStart + text.length
    if (text === '>&' || text === '<&') {
      const duplicatedFd = /\d+-?|-/y
      duplicatedFd.lastIndex = end
      const dupMatch = duplicatedFd.exec(script)
      if (dupMatch && (end + dupMatch[0].length >= n || WORD_BOUNDARY.test(script.charAt(end + dupMatch[0].length)))) {
        end += dupMatch[0].length
      }
    }
    return { start: at, end, text }
  }

  function scanWord(from: number): number {
    let j = from
    while (j < n) {
      const c = script.charAt(j)
      if (c === '\\') {
        j += 2
        continue
      }
      if (WORD_TERMINATOR.test(c)) break
      if (c === '$' && j > from && isExpansionStart(j)) break
      j++
    }
    j = Math.max(from + 1, Math.min(j, n))
    const word = script.slice(from, j)

    if (expectRedirectTarget) {
      expectRedirectTarget = false
      return j
    }
    if (expectCommand && isWordStart(from)) {
      commandStarts.push(from)
      const assignment = ASSIGNMENT.exec(word)
      if (assignment) {
        const equals = from + assignment[0].length - 1
        push(from, equals, 'variable')
        push(equals, equals + 1, 'operator')
        return j
      }
      if (KEYWORDS.has(word)) {
        push(from, j, 'keyword')
        expectCommand = COMMAND_PREFIX_KEYWORDS.has(word)
        return j
      }
      push(from, j, 'command')
      expectCommand = false
      return j
    }
    if (word.startsWith('-') && isWordStart(from)) push(from, j, 'option')
    return j
  }

  // A quote or expansion that starts a new word consumes the command
  // position; one glued to a preceding word (e.g. `FOO="x"`) does not.
  function consumeWordPosition(at: number) {
    if (isWordStart(at)) {
      if (expectCommand && !expectRedirectTarget) commandStarts.push(at)
      expectCommand = false
    }
    expectRedirectTarget = false
  }

  // Inside a quoted substitution, returns at its unmatched closing ")".
  function scanCode(from: number): number {
    let i = from
    while (i < n) {
      const c = script.charAt(i)
      if (c === '\n') {
        i++
        if (pendingHeredocs.length > 0) i = consumeHeredocBodies(i)
        expectCommand = true
        expectRedirectTarget = false
        continue
      }
      if (c === ' ' || c === '\t' || c === '\r') {
        i++
        continue
      }
      if (c === '\\' && script.charAt(i + 1) === '\n') {
        push(i, i + 1, 'operator')
        i += 2
        continue
      }
      if (c === '#' && isWordStart(i)) {
        const newline = script.indexOf('\n', i)
        const end = newline === -1 ? n : newline
        push(i, end, 'comment')
        i = end
        continue
      }
      if (c === "'") {
        const close = script.indexOf("'", i + 1)
        const end = close === -1 ? n : close + 1
        push(i, end, 'string')
        consumeWordPosition(i)
        i = end
        continue
      }
      if (c === '"') {
        consumeWordPosition(i)
        i = scanDoubleQuoted(i)
        continue
      }
      if (c === '`') {
        push(i, i + 1, 'operator')
        i++
        expectCommand = true
        continue
      }
      if (c === '$') {
        if (script.charAt(i + 1) === '(') {
          push(i, i + 2, 'operator')
          groupStack.push(isWordStart(i) ? false : expectCommand)
          expectCommand = true
          i += 2
          continue
        }
        const end = variableEnd(i)
        if (end > i + 1) {
          push(i, end, 'variable')
          consumeWordPosition(i)
          i = end
          continue
        }
      }
      const operator = matchOperator(i)
      if (operator) {
        push(operator.start, operator.end, 'operator')
        i = operator.end
        if (operator.text === '<<' || operator.text === '<<-') {
          heredocStarts.push(operator.start)
          i = scanHeredocDelimiter(i, operator.text === '<<-')
        } else if (REDIRECTS.has(operator.text)) {
          expectRedirectTarget = !script.endsWith(operator.text, operator.end)
        } else if (operator.text === '(') {
          groupStack.push(false)
          expectCommand = true
        } else if (operator.text === ')') {
          if (groupStack.length === 0 && quotedSubstitutionDepth > 0) return i
          expectCommand = groupStack.pop() ?? false
        } else {
          expectCommand = true
          if (groupStack.length === 0 && quotedSubstitutionDepth === 0 && BREAKABLE_OPERATORS.has(operator.text)) {
            topLevelOperators.push(operator)
          }
        }
        continue
      }
      i = scanWord(i)
    }
    return n
  }

  scanCode(0)
  return { tokens, topLevelOperators, heredocStarts, commandStarts }
}

interface Replacement extends TextRange {
  text: string
}

/**
 * Splits physical lines longer than maxLineLength at their top-level list and
 * pipeline operators. Lines that start a heredoc are left alone so the heredoc
 * body keeps its meaning.
 */
export function computeLineBreaks(script: string, scan: ShellScan, maxLineLength: number): Replacement[] {
  const replacements: Replacement[] = []
  for (const op of scan.topLevelOperators) {
    const lineStart = script.lastIndexOf('\n', op.start - 1) + 1
    const newline = script.indexOf('\n', op.end)
    const lineEnd = newline === -1 ? script.length : newline
    if (lineEnd - lineStart <= maxLineLength) continue
    if (scan.heredocStarts.some((start) => start >= lineStart && start < op.start)) continue

    let after = op.end
    while (script.charAt(after) === ' ' || script.charAt(after) === '\t') after++
    if (after >= lineEnd || script.charAt(after) === '#' || script.startsWith('\\\n', after)) continue
    let before = op.start
    while (before > lineStart && /[ \t]/.test(script.charAt(before - 1))) before--
    if (before === lineStart) continue

    if (op.text === ';') {
      replacements.push({ start: op.end, end: after, text: '\n' })
    } else {
      replacements.push({ start: before, end: op.start, text: ' \\\n  ' })
    }
  }
  return replacements
}

export interface MessagePart {
  text: string
  code?: boolean
}

export interface ApprovalTarget {
  id: number
  outcome: PermissionResult
  label: string
  command: string
  summary: MessagePart[]
  detail?: string
  evaluation?: CommandPermissionEvaluation
  located: boolean
}

export interface ScriptMark extends TextRange {
  targetId: number
}

export interface PermissionAnnotation {
  targets: ApprovalTarget[]
  marks: ScriptMark[]
  autoApproved: CommandPermissionEvaluation[]
  notes: string[]
}

const FACTOR_LABELS: Record<string, string> = {
  absolute_path_escalation: 'Absolute path',
  no_rule_matched: 'No rule matched',
  sandbox_default_auto_approve: 'Sandbox default',
  heredoc_file_write_deny: 'Heredoc file write',
  sandbox_heredoc_advisory: 'Heredoc file write',
  heredoc_escape_hatch: 'Heredoc escape hatch',
  temp_path_advisory: 'Temp path',
  empty_command_extraction: 'No commands extracted',
}

const OUTCOME_LABELS: Record<PermissionResult, string> = {
  auto_approve: 'Auto-approve',
  require_approval: 'Require approval',
  deny: 'Deny',
}

export function humanize(value: string): string {
  return value.replace(/_/g, ' ')
}

function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

export function factorLabel(kind: string): string {
  return FACTOR_LABELS[kind] ?? capitalize(humanize(kind))
}

export function outcomeLabel(outcome: PermissionResult): string {
  return OUTCOME_LABELS[outcome] ?? capitalize(humanize(outcome))
}

const code = (text: string): MessagePart => ({ text, code: true })
const plain = (text: string): MessagePart => ({ text })

function isBlocking(outcome: PermissionResult | undefined): outcome is 'require_approval' | 'deny' {
  return outcome === 'require_approval' || outcome === 'deny'
}

function decidingFactor(cmd: CommandPermissionEvaluation): PermissionFactor | undefined {
  return cmd.decidedBy === 'factor' ? cmd.factors?.[cmd.decidedByIndex] : undefined
}

function describeBlockedCommand(cmd: CommandPermissionEvaluation): Pick<ApprovalTarget, 'label' | 'summary' | 'detail'> {
  if (cmd.decidedBy === 'rule') {
    const rule = cmd.matchedRules?.[cmd.decidedByIndex]
    if (rule) {
      return {
        label: 'Rule',
        summary: [
          plain('Rule '),
          code(rule.pattern),
          plain(`${rule.source ? ` (${humanize(rule.source)})` : ''} ${cmd.outcome === 'deny' ? 'denies this command' : 'requires approval'}`),
        ],
        detail: rule.message || undefined,
      }
    }
  }

  const factor = decidingFactor(cmd)
  if (!factor) {
    return { label: outcomeLabel(cmd.outcome), summary: [plain(`${outcomeLabel(cmd.outcome)} required`)] }
  }
  switch (factor.kind) {
    case 'absolute_path_escalation': {
      const autoRule = cmd.matchedRules?.find((rule) => rule.action === 'auto_approve')
      return {
        label: factorLabel(factor.kind),
        summary: autoRule
          ? [plain('Absolute path overrides auto-approve rule '), code(autoRule.pattern)]
          : [plain('Absolute path requires approval')],
      }
    }
    case 'no_rule_matched':
      return { label: factorLabel(factor.kind), summary: [plain('No permission rule matched')] }
    case 'heredoc_escape_hatch':
      return {
        label: 'Heredoc file write',
        summary: [plain('Heredoc file write via '), code('ESCAPE_HATCH_EOF'), plain(' requires approval')],
      }
    default:
      return {
        label: factorLabel(factor.kind),
        summary: [plain(capitalize(factor.message ?? factorLabel(factor.kind)))],
      }
  }
}

/** Explains why an auto-approved command needed no approval. */
export function describeAutoApproval(cmd: CommandPermissionEvaluation): MessagePart[] {
  if (cmd.decidedBy === 'rule') {
    const rule = cmd.matchedRules?.[cmd.decidedByIndex]
    if (rule) {
      return [plain('Auto-approve rule '), code(rule.pattern), plain(rule.source ? ` (${humanize(rule.source)})` : '')]
    }
  }
  const factor = decidingFactor(cmd)
  if (factor?.kind === 'sandbox_default_auto_approve') {
    return [plain('No permission rule matched; unmatched commands auto-approve in this sandbox')]
  }
  return [plain(factor?.message ? capitalize(factor.message) : 'Auto-approved')]
}

export interface PermissionCheck {
  outcome?: PermissionResult
  parts: MessagePart[]
  detail?: string
  decided: boolean
}

/** Lists every matched rule and factor of a command, marking the decider. */
export function describeChecks(cmd: CommandPermissionEvaluation): PermissionCheck[] {
  const rules = (cmd.matchedRules ?? []).map((rule, index): PermissionCheck => ({
    outcome: rule.action,
    parts: [code(rule.pattern), ...(rule.source ? [plain(humanize(rule.source))] : [])],
    detail: rule.message || undefined,
    decided: cmd.decidedBy === 'rule' && cmd.decidedByIndex === index,
  }))
  const factors = (cmd.factors ?? []).map((factor, index): PermissionCheck => ({
    outcome: factor.outcome,
    parts: [plain(factorLabel(factor.kind)), ...(factor.paths ?? []).map(code)],
    detail: factor.message ? capitalize(factor.message) : undefined,
    decided: cmd.decidedBy === 'factor' && cmd.decidedByIndex === index,
  }))
  return [...rules, ...factors]
}

const COMMAND_TERMINATOR = /[;&|)\n`]/

/** Whether an executed command can end at `end`, rather than continuing with more words. */
function endsCommand(script: string, end: number): boolean {
  let j = end
  for (;;) {
    while (/[ \t\r]/.test(script.charAt(j))) j++
    if (!script.startsWith('\\\n', j)) break
    j += 2
  }
  if (j >= script.length) return true
  const next = script.charAt(j)
  return COMMAND_TERMINATOR.test(next) || (next === '#' && j > end)
}

/**
 * Every place `text` is actually executed: it must begin at a command
 * position and end at a command boundary, so identical text inside quoted
 * arguments, comments, heredoc bodies or longer commands never matches.
 */
function executedRanges(script: string, text: string, commandStarts: Set<number>): TextRange[] {
  const variants = [text, text.replace(/\s*&$/, '')].filter((variant, i, all) => variant && all.indexOf(variant) === i)
  const ranges: TextRange[] = []
  for (const variant of variants) {
    for (let start = script.indexOf(variant, 0); start !== -1; start = script.indexOf(variant, start + 1)) {
      const end = start + variant.length
      if (commandStarts.has(start) && endsCommand(script, end)) ranges.push({ start, end })
    }
  }
  return ranges.sort((a, b) => a.start - b.start)
}

/**
 * Assigns each extracted command at most one executed location such that
 * starts strictly increase in extraction order, locating as many commands as
 * possible. When several assignments locate equally many, earlier commands
 * are the ones left unlocated: an inner command that has no executable
 * position of its own (e.g. the code of `sh -c '...'`) directly follows its
 * parent, so a later executable copy of its text belongs to a later command.
 */
function locateCommands(script: string, commands: string[]): (TextRange | null)[] {
  const commandStarts = new Set(scanShell(script).commandStarts)
  const candidates = commands.map((command) => executedRanges(script, command, commandStarts))
  const memo = new Map<number, number>()

  // Most commands from index i onward that can be located with starts > after.
  const best = (i: number, after: number): number => {
    if (i >= commands.length) return 0
    const key = i * (script.length + 1) + after + 1
    const cached = memo.get(key)
    if (cached !== undefined) return cached
    let result = best(i + 1, after)
    for (const range of candidates[i] ?? []) {
      if (range.start > after) result = Math.max(result, 1 + best(i + 1, range.start))
    }
    memo.set(key, result)
    return result
  }

  const located: (TextRange | null)[] = []
  let after = -1
  for (let i = 0; i < commands.length; i++) {
    const target = best(i, after)
    const range = target === best(i + 1, after)
      ? undefined
      : candidates[i]?.find((r) => r.start > after && 1 + best(i + 1, r.start) === target)
    located.push(range ?? null)
    if (range) after = range.start
  }
  return located
}

function occurrencesWithin(script: string, needle: string, range: TextRange): TextRange[] {
  const found: TextRange[] = []
  let at = script.indexOf(needle, range.start)
  while (needle && at !== -1 && at + needle.length <= range.end) {
    found.push({ start: at, end: at + needle.length })
    at = script.indexOf(needle, at + needle.length)
  }
  return found
}

function trimmedRange(script: string): TextRange {
  const start = script.length - script.trimStart().length
  return { start, end: start + script.trim().length }
}

/**
 * Maps a script's permission evaluation onto the script text: what needs
 * approval (targets) and where it is (marks).
 *
 * Extracted commands are located at executed command positions in extraction
 * order. The extractor walks the syntax tree depth-first, so each command
 * starts after the previous command's start and nested commands fall inside
 * their parent. Commands that cannot be placed verbatim at such a position
 * (e.g. code passed to `sh -c`) are reported as unlocated targets.
 */
export function annotatePermissionEvaluation(evaluation: ScriptPermissionEvaluation, script: string): PermissionAnnotation {
  const annotation: PermissionAnnotation = { targets: [], marks: [], autoApproved: [], notes: [] }

  for (const factor of evaluation.factors ?? []) {
    if (isBlocking(factor.outcome)) {
      const id = annotation.targets.length
      annotation.targets.push({
        id,
        outcome: factor.outcome,
        label: factorLabel(factor.kind),
        command: script,
        summary: [plain(factor.kind === 'empty_command_extraction'
          ? 'No commands could be extracted from the script'
          : capitalize(factor.message ?? factorLabel(factor.kind)))],
        located: true,
      })
      annotation.marks.push({ ...trimmedRange(script), targetId: id })
    } else if (factor.message) {
      annotation.notes.push(factor.message)
    }
  }

  const commands = evaluation.commands ?? []
  const locations = locateCommands(script, commands.map((cmd) => cmd.command))
  for (const [index, cmd] of commands.entries()) {
    const range = locations[index] ?? null

    if (!isBlocking(cmd.outcome)) {
      annotation.autoApproved.push(cmd)
      continue
    }

    const id = annotation.targets.length
    annotation.targets.push({ id, outcome: cmd.outcome, command: cmd.command, evaluation: cmd, located: range !== null, ...describeBlockedCommand(cmd) })
    if (!range) continue

    const factor = decidingFactor(cmd)
    const pathRanges = factor?.kind === 'absolute_path_escalation'
      ? [...new Set(factor.paths ?? [])].flatMap((path) => occurrencesWithin(script, path, range))
      : []
    for (const markRange of pathRanges.length > 0 ? pathRanges : [range]) {
      annotation.marks.push({ ...markRange, targetId: id })
    }
  }

  return annotation
}

export interface AnnotatedSegment {
  text: string
  kind?: ShellTokenKind
  /** Innermost approval target covering this text. */
  targetId?: number
  /** Number of nested approval targets covering this text. */
  depth: number
}

export interface AnnotatedLine {
  segments: AnnotatedSegment[]
  /** Approval targets with a mark starting on this line. */
  targetIds: number[]
}

/**
 * Formats and highlights a script, overlaying approval marks, and splits the
 * result into display lines.
 */
export function layoutAnnotatedScript(script: string, marks: ScriptMark[], maxLineLength = 80): AnnotatedLine[] {
  const scan = scanShell(script)
  const replacements = computeLineBreaks(script, scan, maxLineLength)
  const points = new Set([0, script.length])
  for (const range of [...scan.tokens, ...marks, ...replacements]) {
    points.add(range.start)
    points.add(range.end)
  }
  const boundaries = [...points].filter((p) => p >= 0 && p <= script.length).sort((a, b) => a - b)

  const lines: AnnotatedLine[] = [{ segments: [], targetIds: [] }]
  const currentLine = () => lines[lines.length - 1]!
  const append = (text: string, segment: Omit<AnnotatedSegment, 'text'>) => {
    text.split('\n').forEach((part, index) => {
      if (index > 0) lines.push({ segments: [], targetIds: [] })
      if (!part) return
      const segments = currentLine().segments
      const last = segments[segments.length - 1]
      if (last && last.kind === segment.kind && last.targetId === segment.targetId && last.depth === segment.depth) {
        last.text += part
      } else {
        segments.push({ text: part, ...segment })
      }
    })
  }

  let tokenIndex = 0
  let replacementIndex = 0
  for (let b = 0; b < boundaries.length - 1; b++) {
    const start = boundaries[b]!
    const end = boundaries[b + 1]!
    while (tokenIndex < scan.tokens.length && scan.tokens[tokenIndex]!.end <= start) tokenIndex++
    while (replacementIndex < replacements.length && replacements[replacementIndex]!.end <= start) replacementIndex++
    const token = scan.tokens[tokenIndex]
    const replacement = replacements[replacementIndex]

    const active = marks.filter((mark) => mark.start <= start && start < mark.end)
    const innermost = active.reduce<ScriptMark | undefined>(
      (best, mark) => (!best || mark.end - mark.start <= best.end - best.start ? mark : best),
      undefined,
    )
    for (const mark of marks) {
      const line = currentLine()
      if (mark.start === start && !line.targetIds.includes(mark.targetId)) line.targetIds.push(mark.targetId)
    }

    if (replacement && replacement.start <= start) {
      if (replacement.start === start) {
        append(replacement.text, { targetId: innermost?.targetId, depth: active.length })
      }
      continue
    }
    append(script.slice(start, end), {
      kind: token && token.start <= start ? token.kind : undefined,
      targetId: innermost?.targetId,
      depth: active.length,
    })
  }

  return lines
}