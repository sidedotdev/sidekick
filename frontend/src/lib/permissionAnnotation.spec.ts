import { describe, it, expect } from 'vitest'
import {
  annotatePermissionEvaluation,
  computeLineBreaks,
  layoutAnnotatedScript,
  scanShell,
  type AnnotatedLine,
  type ShellTokenKind,
} from './permissionAnnotation'
import type { CommandPermissionEvaluation, ScriptPermissionEvaluation } from './models'

function tokensOf(script: string, kind: ShellTokenKind): string[] {
  return scanShell(script).tokens.filter((t) => t.kind === kind).map((t) => script.slice(t.start, t.end))
}

function lineTexts(lines: AnnotatedLine[]): string[] {
  return lines.map((line) => line.segments.map((s) => s.text).join(''))
}

function flaggedTexts(lines: AnnotatedLine[]): Record<number, string> {
  const result: Record<number, string> = {}
  for (const line of lines) {
    for (const s of line.segments) {
      if (s.targetId !== undefined) result[s.targetId] = (result[s.targetId] ?? '') + s.text
    }
  }
  return result
}

const auto = (command: string, pattern: string): CommandPermissionEvaluation => ({
  command,
  outcome: 'auto_approve',
  matchedRules: [{ action: 'auto_approve', pattern, source: 'base' }],
  decidedBy: 'rule',
  decidedByIndex: 0,
})

const noRule = (command: string): CommandPermissionEvaluation => ({
  command,
  outcome: 'require_approval',
  factors: [{ kind: 'no_rule_matched', outcome: 'require_approval', message: 'no permission rule matched' }],
  decidedBy: 'factor',
  decidedByIndex: 0,
})

const absolutePath = (command: string, pattern: string, paths: string[]): CommandPermissionEvaluation => ({
  command,
  outcome: 'require_approval',
  matchedRules: [{ action: 'auto_approve', pattern, source: 'base' }],
  factors: [{ kind: 'absolute_path_escalation', outcome: 'require_approval', paths }],
  decidedBy: 'factor',
  decidedByIndex: 0,
})

describe('scanShell', () => {
  it.concurrent.each([
    ['command names after operators', 'cd repo && go test ./... | tee out.txt', 'command', ['cd', 'go', 'tee']],
    ['options', 'grep -c --color=never x', 'option', ['-c', '--color=never']],
    ['quoted strings', `echo 'a b' "c"`, 'string', [`'a b'`, '"c"']],
    ['variables, including inside double quotes', 'echo $HOME "${USER}x"', 'variable', ['$HOME', '${USER}']],
    ['assignments prefixing a command', 'FOO="x y" make build', 'command', ['make']],
    ['commands inside quoted substitutions', 'echo "$(git rev-parse HEAD)"', 'command', ['echo', 'git']],
    ['options inside quoted substitutions', 'echo "$(git rev-parse --short HEAD)"', 'option', ['--short']],
    ['quoted text around substitutions', 'echo "at $(date) now"', 'string', ['"at ', ' now"']],
    [
      'commands in nested quoted substitutions',
      'deploy --target="$(resolve --account="$(whoami)")" --force',
      'command',
      ['deploy', 'resolve', 'whoami'],
    ],
    ['comments only at word starts', 'echo a#b # trailing', 'comment', ['# trailing']],
    ['fd redirections as operators', 'go test 2>&1 >out', 'operator', ['2>&1', '>']],
    ['commands after leading redirections', '2>errors.log git status', 'command', ['git']],
    ['commands after leading quoted redirections', '> "output.log" git status', 'command', ['git']],
    ['commands after fd duplication', '2>&1 git status', 'command', ['git']],
    ['commands in redirection targets', 'git status >"$(get_log_path)"', 'command', ['git', 'get_log_path']],
    ['commands in backtick redirection targets', 'git status >`get_log_path`', 'command', ['git', 'get_log_path']],
  ])('recognizes %s', (_name, script, kind, expected) => {
    expect(tokensOf(script, kind as ShellTokenKind)).toEqual(expected)
  })

  it('treats heredoc bodies as content rather than commands', () => {
    const script = "cat > f.sh <<'EOF'\nrm -rf / && echo hi\nEOF\ngit status"
    expect(tokensOf(script, 'heredoc')).toEqual(['rm -rf / && echo hi\n'])
    expect(tokensOf(script, 'command')).toEqual(['cat', 'git'])
    expect(tokensOf(script, 'string')).toEqual(["'EOF'", 'EOF'])
  })

  it('only reports operators outside subshells, substitutions and quotes as top level', () => {
    const script = 'a && (b || c) && echo "$(d | e)" "x;y"; f'
    expect(scanShell(script).topLevelOperators.map((op) => op.text)).toEqual(['&&', '&&', ';'])
  })
})

describe('layoutAnnotatedScript formatting', () => {
  it('leaves short lines untouched', () => {
    const script = 'cd repo && go test ./...'
    expect(lineTexts(layoutAnnotatedScript(script, []))).toEqual([script])
  })

  it('splits long lines at top-level operators', () => {
    const script = 'cd /home/me/repo && go test ./... 2>&1 | tee /tmp/out.txt && grep -c FAIL out.txt; rm -rf build'
    expect(lineTexts(layoutAnnotatedScript(script, []))).toEqual([
      'cd /home/me/repo \\',
      '  && go test ./... 2>&1 \\',
      '  | tee /tmp/out.txt \\',
      '  && grep -c FAIL out.txt;',
      'rm -rf build',
    ])
  })

  it('does not reflow lines that start a heredoc', () => {
    const script = `cat > out.txt <<EOF && echo ${'x'.repeat(80)}\nbody && more\nEOF`
    expect(computeLineBreaks(script, scanShell(script), 80)).toEqual([])
  })
})

describe('annotatePermissionEvaluation', () => {
  it('flags only what was not approved and highlights absolute paths within their command', () => {
    const script = 'cd /home/me/repo && go test ./... && mytool sync'
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [
        absolutePath('cd /home/me/repo', '^cd\\b', ['/home/me/repo']),
        auto('go test ./...', '^go test\\b'),
        noRule('mytool sync'),
      ],
    }

    const annotation = annotatePermissionEvaluation(evaluation, script)

    expect(annotation.targets.map((t) => t.command)).toEqual(['cd /home/me/repo', 'mytool sync'])
    expect(annotation.autoApproved.map((c) => c.command)).toEqual(['go test ./...'])
    expect(annotation.targets[0]!.summary).toEqual([
      { text: 'Absolute path overrides auto-approve rule ' },
      { text: '^cd\\b', code: true },
    ])
    expect(annotation.targets[1]!.summary).toEqual([{ text: 'No permission rule matched' }])
    expect(flaggedTexts(layoutAnnotatedScript(script, annotation.marks))).toEqual({ 0: '/home/me/repo', 1: 'mytool sync' })
  })

  it.concurrent.each([false, true])('highlights checkout in the reported script (continued lines: %s)', (continuedLines) => {
    const displayedScript = String.raw`cd .side/tmp/builderhub \
  && git checkout -q main 2>/dev/null;
grep -rn "initialize" src server --include=*.ts --include=*.tsx --include=*.go \
  | grep -v test \
  | grep -i "command\|initializeConfigChat" \
  | head;
cd - >/dev/null;
grep -rln "command=initialize\|\"initialize\"\|MUPPET_COMMAND_INITIALIZE" --include=*.go --include=*.py --include=*.ts --include=*.sh --include=Dockerfile . \
  | grep -v "^./.side\|node_modules" \
  | head`
    const script = continuedLines ? displayedScript : displayedScript.replace(/\\\n\s*/g, '').replace(/\n/g, ' ')
    const command = 'git checkout -q main'
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [
        auto('cd .side/tmp/builderhub', '^cd\\b'),
        noRule(command),
        auto('grep -rn "initialize" src server --include=*.ts --include=*.tsx --include=*.go', '^grep\\b'),
        auto('grep -v test', '^grep\\b'),
        auto(String.raw`grep -i "command\|initializeConfigChat"`, '^grep\\b'),
        auto('head', '^head\\b'),
        auto('cd - >/dev/null', '^cd\\b'),
        auto(String.raw`grep -rln "command=initialize\|\"initialize\"\|MUPPET_COMMAND_INITIALIZE" --include=*.go --include=*.py --include=*.ts --include=*.sh --include=Dockerfile .`, '^grep\\b'),
        auto(String.raw`grep -v "^./.side\|node_modules"`, '^grep\\b'),
        auto('head', '^head\\b'),
      ],
    }

    const annotation = annotatePermissionEvaluation(evaluation, script)
    const start = script.indexOf(command)

    expect(annotation.targets).toMatchObject([{ command, located: true }])
    expect(annotation.marks).toEqual([{ start, end: start + command.length, targetId: 0 }])
    expect(flaggedTexts(layoutAnnotatedScript(script, annotation.marks))).toEqual({ 0: command })
  })

  it('locates repeated commands in extraction order', () => {
    const script = 'ls /etc && ls'
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [auto('ls /etc', '^ls\\b'), noRule('ls')],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    expect(annotation.marks).toEqual([{ start: 11, end: 13, targetId: 0 }])
  })

  it.concurrent.each([
    '2>/dev/null',
    '>/dev/null',
    '>>output.log 2>&1',
    '<input.txt >output.txt',
    '&>output.log',
    '2> "errors with spaces.log"',
    '2>errors.log \\\n  >output.log',
    '2>errors.log"$SUFFIX"',
    '2>"$(printf \'%s\' errors.log)"',
    '2>$(printf \'%s\' errors.log)',
    '2>`printf \'%s\' errors.log`',
    '2>&"$FD"',
    '0<&3 2>&- 1>&2-',
    '<<<"main"',
    "<<'EOF'\nmain\nEOF",
  ])('locates commands followed by trailing redirections: %s', (redirections) => {
    const command = 'git checkout -q main'
    const script = `${command} ${redirections}\ngit status`
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [noRule(command), auto('git status', '^git status\\b')],
    }

    const annotation = annotatePermissionEvaluation(evaluation, script)

    expect(annotation.targets).toMatchObject([{ command, located: true }])
    expect(annotation.marks).toEqual([{ start: 0, end: command.length, targetId: 0 }])
    expect(flaggedTexts(layoutAnnotatedScript(script, annotation.marks))).toEqual({ 0: command })
  })

  it.concurrent.each([
    ['a quoted argument', 'echo "rm -rf build" && rm -rf build', 'echo "rm -rf build"'],
    ['a single-quoted argument', "echo 'rm -rf build' && rm -rf build", "echo 'rm -rf build'"],
    ['a comment', '# rm -rf build first\nrm -rf build', undefined],
    ['a heredoc body', "cat > notes <<'EOF'\nrm -rf build\nEOF\nrm -rf build", "cat > notes <<'EOF'\nrm -rf build\nEOF"],
    ['a longer command', 'sudo rm -rf build/cache && rm -rf build', 'sudo rm -rf build/cache'],
  ])('marks the executed command rather than identical text in %s', (_name, script, approvedCommand) => {
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [...(approvedCommand ? [auto(approvedCommand, '^\\w+\\b')] : []), noRule('rm -rf build')],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    const start = script.lastIndexOf('rm -rf build')
    expect(annotation.targets).toMatchObject([{ command: 'rm -rf build', located: true }])
    expect(annotation.marks).toEqual([{ start, end: start + 'rm -rf build'.length, targetId: 0 }])
  })

  it('leaves a command unlocated when it only appears inside quoted text', () => {
    const script = 'echo "rm -rf build"'
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [noRule('rm -rf build')],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    expect(annotation.targets).toMatchObject([{ command: 'rm -rf build', located: false }])
    expect(annotation.marks).toEqual([])
  })

  it.concurrent.each([
    'git checkout -q main 2>/dev/null other-branch',
    'git checkout -q main>output.log other-branch',
    'git checkout -q main 2>errors.log" suffix" other-branch',
    'git checkout -q main 2>errors.log#not-a-comment other-branch',
    'git checkout -q main &>output.log other-branch',
    'git checkout -q main 2>"$(printf \'%s\' errors.log)" other-branch',
    'git checkout -q main 2>`printf \'%s\' errors.log` other-branch',
    'echo "git checkout -q main 2>/dev/null"',
    '# git checkout -q main 2>/dev/null',
    "cat <<'EOF'\ngit checkout -q main 2>/dev/null\nEOF",
  ])('does not locate an incomplete or non-executed command before a redirection: %s', (script) => {
    const command = 'git checkout -q main'
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [noRule(command)],
    }

    const annotation = annotatePermissionEvaluation(evaluation, script)

    expect(annotation.targets).toMatchObject([{ command, located: false }])
    expect(annotation.marks).toEqual([])
  })

  it('does not let an unlocatable sh -c inner command take a later top-level copy', () => {
    const script = "sh -c 'rm -rf build' && rm -rf build"
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [auto("sh -c 'rm -rf build'", '^sh\\b'), noRule('rm -rf build'), noRule('rm -rf build')],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    const start = script.lastIndexOf('rm -rf build')
    expect(annotation.targets).toMatchObject([
      { id: 0, command: 'rm -rf build', located: false },
      { id: 1, command: 'rm -rf build', located: true },
    ])
    expect(annotation.marks).toEqual([{ start, end: start + 'rm -rf build'.length, targetId: 1 }])
  })

  it('locates the top-level command preceding an sh -c with the same inner command', () => {
    const script = "rm -rf build && sh -c 'rm -rf build'"
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [noRule('rm -rf build'), noRule("sh -c 'rm -rf build'"), noRule('rm -rf build')],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    expect(annotation.targets.map((t) => t.located)).toEqual([true, true, false])
    expect(annotation.marks).toEqual([
      { start: 0, end: 12, targetId: 0 },
      { start: 16, end: script.length, targetId: 1 },
    ])
  })

  it('locates a nested command and a repeated top-level copy separately', () => {
    const script = 'echo "$(whoami)" && whoami'
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [auto('echo "$(whoami)"', '^echo\\b'), noRule('whoami'), noRule('whoami')],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    expect(annotation.marks).toEqual([
      { start: 8, end: 14, targetId: 0 },
      { start: 20, end: 26, targetId: 1 },
    ])
  })

  it('nests inner command substitutions inside the outer command', () => {
    const script = 'deploy --target="$(resolve --account="$(whoami)")" --force'
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [
        noRule(script),
        noRule('resolve --account="$(whoami)"'),
        noRule('whoami'),
      ],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    const lines = layoutAnnotatedScript(script, annotation.marks)
    const segments = lines.flatMap((line) => line.segments)

    expect(segments.find((s) => s.text.includes('whoami'))).toMatchObject({ targetId: 2, depth: 3 })
    expect(segments.find((s) => s.text.includes('resolve'))).toMatchObject({ targetId: 1, depth: 2 })
    expect(segments.find((s) => s.text.includes('deploy'))).toMatchObject({ targetId: 0, depth: 1 })
    expect(lines[0]!.targetIds).toEqual([0, 1, 2])
  })

  it('reports the rule and its message when a rule requires approval', () => {
    const script = 'rm -rf build'
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [{
        command: 'rm -rf build',
        outcome: 'require_approval',
        matchedRules: [{ action: 'require_approval', pattern: '^rm\\b', source: 'repo_config', message: 'rm is risky' }],
        decidedBy: 'rule',
        decidedByIndex: 0,
      }],
    }
    const [target] = annotatePermissionEvaluation(evaluation, script).targets
    expect(target!.summary.map((p) => p.text).join('')).toBe('Rule ^rm\\b (repo config) requires approval')
    expect(target!.detail).toBe('rm is risky')
  })

  it('keeps commands that cannot be located verbatim as unlocated targets', () => {
    const script = `sh -c "echo \\"hi\\""`
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      commands: [auto(script, '^sh\\b'), noRule('echo "hi"')],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    expect(annotation.targets).toMatchObject([{ command: 'echo "hi"', located: false }])
    expect(annotation.marks).toEqual([])
  })

  it('flags the whole script for blocking script-level factors and keeps advisories as notes', () => {
    const script = '  $(true)  '
    const evaluation: ScriptPermissionEvaluation = {
      outcome: 'require_approval',
      factors: [
        { kind: 'temp_path_advisory', message: 'Prefer .side/tmp over /tmp' },
        { kind: 'empty_command_extraction', outcome: 'require_approval', message: 'no commands were extracted' },
      ],
    }
    const annotation = annotatePermissionEvaluation(evaluation, script)
    expect(annotation.notes).toEqual(['Prefer .side/tmp over /tmp'])
    expect(annotation.targets).toMatchObject([{ label: 'No commands extracted' }])
    expect(annotation.marks).toEqual([{ start: 2, end: 9, targetId: 0 }])
  })
})