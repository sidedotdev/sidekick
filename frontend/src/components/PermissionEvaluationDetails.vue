<template>
  <div class="permission-evaluation">
    <div class="perm-header">
      <span class="perm-title">Permission evaluation</span>
      <span class="perm-outcome" :class="outcomeClass(evaluation.outcome)">{{ evaluation.outcome }}</span>
      <button type="button" class="perm-toggle" @click.stop="showDetails = !showDetails">
        {{ showDetails ? 'Hide details' : 'Show details' }}
      </button>
    </div>
    <ul class="perm-summary-list">
      <li v-for="(cmd, i) in evaluation.commands ?? []" :key="i" class="perm-summary-item">
        <span class="perm-outcome" :class="outcomeClass(cmd.outcome)">{{ cmd.outcome }}</span>
        <code class="perm-command-text">{{ cmd.command }}</code>
        <span class="perm-decider">{{ deciderSummary(cmd) }}</span>
      </li>
    </ul>
    <div v-if="showDetails" class="perm-details">
      <div v-for="(cmd, i) in evaluation.commands ?? []" :key="i" class="perm-command-details">
        <code class="perm-command-text">{{ cmd.command }}</code>
        <div v-if="cmd.matchedRules?.length" class="perm-section">
          <div class="perm-section-title">Matched rules</div>
          <ul>
            <li
              v-for="(rule, ri) in cmd.matchedRules"
              :key="ri"
              :class="{ 'perm-decided': cmd.decidedBy === 'rule' && cmd.decidedByIndex === ri }"
            >
              <span class="perm-outcome" :class="outcomeClass(rule.action)">{{ rule.action }}</span>
              <code class="perm-pattern">{{ rule.pattern }}</code>
              <span v-if="rule.source" class="perm-source">{{ humanize(rule.source) }}</span>
              <span v-if="rule.message" class="perm-message">{{ rule.message }}</span>
            </li>
          </ul>
        </div>
        <div v-if="cmd.factors?.length" class="perm-section">
          <div class="perm-section-title">Factors</div>
          <ul>
            <li
              v-for="(factor, fi) in cmd.factors"
              :key="fi"
              :class="{ 'perm-decided': cmd.decidedBy === 'factor' && cmd.decidedByIndex === fi }"
            >
              <span v-if="factor.outcome" class="perm-outcome" :class="outcomeClass(factor.outcome)">{{ factor.outcome }}</span>
              <span class="perm-factor-kind">{{ humanize(factor.kind) }}</span>
              <code v-if="factor.paths?.length" class="perm-pattern">{{ factor.paths.join(', ') }}</code>
              <span v-if="factor.message" class="perm-message">{{ factor.message }}</span>
            </li>
          </ul>
        </div>
      </div>
      <div v-if="evaluation.factors?.length" class="perm-section">
        <div class="perm-section-title">Script factors</div>
        <ul>
          <li v-for="(factor, fi) in evaluation.factors" :key="fi">
            <span v-if="factor.outcome" class="perm-outcome" :class="outcomeClass(factor.outcome)">{{ factor.outcome }}</span>
            <span class="perm-factor-kind">{{ humanize(factor.kind) }}</span>
            <code v-if="factor.paths?.length" class="perm-pattern">{{ factor.paths.join(', ') }}</code>
            <span v-if="factor.message" class="perm-message">{{ factor.message }}</span>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { CommandPermissionEvaluation, PermissionResult, ScriptPermissionEvaluation } from '../lib/models'

defineProps<{
  evaluation: ScriptPermissionEvaluation
}>()

const showDetails = ref(false)

function outcomeClass(outcome: PermissionResult): string {
  switch (outcome) {
    case 'auto_approve':
      return 'perm-outcome-auto-approve'
    case 'deny':
      return 'perm-outcome-deny'
    default:
      return 'perm-outcome-require-approval'
  }
}

function humanize(value: string): string {
  return value.replace(/_/g, ' ')
}

function deciderSummary(cmd: CommandPermissionEvaluation): string {
  if (cmd.decidedBy === 'rule') {
    const rule = cmd.matchedRules?.[cmd.decidedByIndex]
    if (rule) {
      return rule.source ? `rule ${rule.pattern} (${humanize(rule.source)})` : `rule ${rule.pattern}`
    }
  } else {
    const factor = cmd.factors?.[cmd.decidedByIndex]
    if (factor) {
      return humanize(factor.kind)
    }
  }
  return ''
}
</script>

<style scoped>
.permission-evaluation {
  font-size: 0.85rem;
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.375rem;
  padding: 0.5rem 0.75rem;
  background-color: var(--color-background-soft);
  color: var(--color-text-2);
}

.perm-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.perm-title {
  font-weight: 600;
  color: var(--color-text);
}

.perm-toggle {
  margin-left: auto;
  background: none;
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.25rem;
  color: var(--color-link);
  padding: 0.1rem 0.5rem;
  font-size: 0.8rem;
  cursor: pointer;
}

.perm-toggle:hover {
  background-color: var(--color-background-hover);
}

.permission-evaluation ul {
  list-style: none;
  padding: 0;
  margin: 0.25rem 0 0;
}

.permission-evaluation li {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 0.5rem;
  padding: 0.15rem 0.25rem;
  border-radius: 0.25rem;
}

.perm-outcome {
  font-family: monospace;
  font-size: 0.8em;
  white-space: nowrap;
}

.perm-outcome-auto-approve {
  color: var(--color-green);
}

.perm-outcome-require-approval {
  color: var(--color-text-2);
}

.perm-outcome-deny {
  color: var(--color-error-text);
}

.perm-command-text,
.perm-pattern {
  font-family: monospace;
  overflow-wrap: anywhere;
}

.perm-command-text {
  color: var(--color-text);
}

.perm-decider,
.perm-source,
.perm-factor-kind {
  font-style: italic;
}

.perm-message {
  flex-basis: 100%;
  white-space: pre-wrap;
}

.perm-details {
  margin-top: 0.5rem;
  border-top: 1px solid var(--color-border);
  padding-top: 0.5rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.perm-section-title {
  font-weight: 600;
  color: var(--color-text);
  margin-top: 0.25rem;
}

.perm-decided {
  background-color: var(--color-background-mute);
}
</style>