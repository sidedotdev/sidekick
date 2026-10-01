<template>
  <div class="permission-evaluation">
    <div class="perm-header">
      <span class="perm-headline" :class="{ 'perm-headline-flagged': annotation.targets.length > 0 }">{{ headline }}</span>
      <button type="button" class="perm-toggle" :aria-expanded="showDetails" @click.stop="showDetails = !showDetails">
        {{ showDetails ? 'Hide details' : 'Show details' }}
      </button>
    </div>

    <div
      class="perm-script"
      @mouseover="onPointerOver"
      @mouseleave="scheduleHide"
      @focusin="onFocusIn"
      @focusout="hideTooltip"
    >
      <div v-for="(line, li) in lines" :key="li" class="perm-line">
        <span class="perm-gutter">
          <button
            v-if="line.targetIds.length"
            type="button"
            class="perm-gutter-icon"
            :data-target-ids="line.targetIds.join(',')"
            :aria-label="ariaLabel(line.targetIds)"
          >⚠</button>
        </span>
        <code class="perm-line-code"><span
          v-for="(segment, si) in line.segments"
          :key="si"
          :class="segmentClasses(segment)"
          :data-target-ids="segment.targetId"
        >{{ segment.text }}</span></code>
      </div>
      <template v-if="unlocatedTargets.length">
        <div class="perm-unlocated-caption">Also needs approval</div>
        <div v-for="target in unlocatedTargets" :key="target.id" class="perm-line">
          <span class="perm-gutter">
            <button type="button" class="perm-gutter-icon" :data-target-ids="target.id" :aria-label="ariaLabel([target.id])">⚠</button>
          </span>
          <code class="perm-line-code"><span class="perm-flagged" :data-target-ids="target.id">{{ target.command }}</span></code>
        </div>
      </template>
    </div>

    <div v-if="showDetails" class="perm-details">
      <article v-for="target in annotation.targets" :key="target.id" class="perm-card">
        <div class="perm-card-label" :class="{ 'perm-deny': target.outcome === 'deny' }">
          {{ target.outcome === 'deny' ? 'Denied' : 'Needs approval' }} · {{ target.label }}
        </div>
        <code class="perm-card-command">{{ target.command }}</code>
        <p><MessageParts :parts="target.summary" /></p>
        <p v-if="target.detail" class="perm-muted">{{ target.detail }}</p>
        <PermissionChecks v-if="target.evaluation" :evaluation="target.evaluation" />
      </article>

      <details v-if="annotation.autoApproved.length" class="perm-approved">
        <summary>Auto-approved ({{ annotation.autoApproved.length }})</summary>
        <article v-for="(cmd, ci) in annotation.autoApproved" :key="ci" class="perm-card">
          <code class="perm-card-command">{{ cmd.command }}</code>
          <p><MessageParts :parts="describeAutoApproval(cmd)" /></p>
          <PermissionChecks :evaluation="cmd" />
        </article>
      </details>

      <div v-if="annotation.notes.length" class="perm-notes">
        <div class="perm-notes-title">Notes</div>
        <p v-for="(note, ni) in annotation.notes" :key="ni" class="perm-muted">{{ note }}</p>
      </div>
    </div>

    <Teleport to="body">
      <div
        v-if="tooltip"
        ref="tooltipRef"
        class="perm-tooltip"
        role="tooltip"
        :data-placement="tooltip.placement"
        :style="tooltipStyle"
      >
        <div v-for="target in tooltipTargets" :key="target.id" class="perm-tooltip-entry">
          <div><MessageParts :parts="target.summary" /></div>
          <div v-if="target.detail" class="perm-muted">{{ target.detail }}</div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, nextTick, onMounted, onUnmounted, ref, type PropType } from 'vue'
import type { CommandPermissionEvaluation, PermissionResult, ScriptPermissionEvaluation } from '../lib/models'
import {
  annotatePermissionEvaluation,
  describeAutoApproval,
  describeChecks,
  layoutAnnotatedScript,
  outcomeLabel,
  type AnnotatedSegment,
  type ApprovalTarget,
  type MessagePart,
} from '../lib/permissionAnnotation'

const props = defineProps<{
  evaluation: ScriptPermissionEvaluation
  script: string
}>()

const MessageParts = defineComponent({
  props: { parts: { type: Array as PropType<MessagePart[]>, required: true } },
  setup(componentProps) {
    return () => componentProps.parts.map((part) => (part.code ? h('code', part.text) : part.text))
  },
})

function outcomeClass(outcome?: PermissionResult): string {
  switch (outcome) {
    case 'auto_approve':
      return 'perm-check-auto-approve'
    case 'deny':
      return 'perm-deny'
    case 'require_approval':
      return 'perm-check-require-approval'
    default:
      return 'perm-muted'
  }
}

const PermissionChecks = defineComponent({
  props: { evaluation: { type: Object as PropType<CommandPermissionEvaluation>, required: true } },
  setup(componentProps) {
    return () => {
      const checks = describeChecks(componentProps.evaluation)
      if (checks.length === 0) return null
      return h('ul', { class: 'perm-checks' }, checks.map((check) => h('li', { class: { 'perm-check-decided': check.decided } }, [
        h('span', { class: ['perm-check-outcome', outcomeClass(check.outcome)] }, check.outcome ? outcomeLabel(check.outcome) : 'Advisory'),
        h('span', { class: 'perm-check-parts' }, h(MessageParts, { parts: check.parts })),
        check.decided ? h('span', { class: 'perm-check-decided-tag' }, 'decided') : null,
        check.detail ? h('span', { class: 'perm-check-detail' }, check.detail) : null,
      ])))
    }
  },
})

const showDetails = ref(false)

const annotation = computed(() => annotatePermissionEvaluation(props.evaluation, props.script))
const lines = computed(() => layoutAnnotatedScript(props.script, annotation.value.marks))
const unlocatedTargets = computed(() => annotation.value.targets.filter((target) => !target.located))
const targetsById = computed(() => new Map(annotation.value.targets.map((target) => [target.id, target])))

const headline = computed(() => {
  const denied = annotation.value.targets.filter((target) => target.outcome === 'deny').length
  const needApproval = annotation.value.targets.length - denied
  const parts: string[] = []
  if (denied) parts.push(`${denied} denied`)
  if (needApproval) parts.push(`${needApproval} ${needApproval === 1 ? 'item needs' : 'items need'} approval`)
  return parts.length ? parts.join(' · ') : 'Nothing needs approval'
})

function segmentClasses(segment: AnnotatedSegment): (string | undefined)[] {
  return [
    segment.kind && `tok-${segment.kind}`,
    segment.targetId !== undefined ? 'perm-flagged' : undefined,
    segment.depth > 1 ? `perm-flagged-depth-${Math.min(segment.depth, 3)}` : undefined,
  ]
}

function plainText(parts: MessagePart[]): string {
  return parts.map((part) => part.text).join('')
}

function ariaLabel(targetIds: number[]): string {
  return targetIds
    .map((id) => targetsById.value.get(id))
    .filter((target): target is ApprovalTarget => target !== undefined)
    .map((target) => plainText(target.summary))
    .join('; ')
}

interface TooltipState {
  key: string
  targetIds: number[]
  anchor: HTMLElement
  point?: { x: number; y: number }
  placement: 'top' | 'bottom'
  left: number
  top: number
  arrowX: number
  measured: boolean
}

const TOOLTIP_MARGIN = 8
const TOOLTIP_GAP = 8
const tooltip = ref<TooltipState | null>(null)
const tooltipRef = ref<HTMLElement | null>(null)
let hideTimer: ReturnType<typeof setTimeout> | undefined

const tooltipTargets = computed(() => (tooltip.value?.targetIds ?? [])
  .map((id) => targetsById.value.get(id))
  .filter((target): target is ApprovalTarget => target !== undefined))

const tooltipStyle = computed(() => {
  const state = tooltip.value
  if (!state) return {}
  return {
    left: `${state.left}px`,
    top: `${state.top}px`,
    visibility: state.measured ? 'visible' : 'hidden',
    '--arrow-x': `${state.arrowX}px`,
  } as Record<string, string>
})

function targetElement(event: Event): HTMLElement | null {
  return (event.target as HTMLElement | null)?.closest?.<HTMLElement>('[data-target-ids]') ?? null
}

function parseTargetIds(element: HTMLElement): number[] {
  return (element.dataset.targetIds ?? '').split(',').filter(Boolean).map(Number)
}

function cancelHide() {
  if (hideTimer !== undefined) {
    clearTimeout(hideTimer)
    hideTimer = undefined
  }
}

function hideTooltip() {
  cancelHide()
  tooltip.value = null
}

// Delayed so moving between adjacent segments of one target doesn't flicker.
function scheduleHide() {
  cancelHide()
  hideTimer = setTimeout(hideTooltip, 80)
}

// For a highlight wrapping across lines, anchor to the fragment under the pointer.
function anchorRect(element: HTMLElement, point?: { x: number; y: number }): DOMRect {
  const rects = Array.from(element.getClientRects())
  const hovered = point && rects.find((rect) => point.y >= rect.top && point.y <= rect.bottom)
  return hovered ?? rects[0] ?? element.getBoundingClientRect()
}

async function showTooltip(element: HTMLElement, point?: { x: number; y: number }) {
  cancelHide()
  const targetIds = parseTargetIds(element)
  const key = targetIds.join(',')
  if (tooltip.value?.key === key) return
  tooltip.value = { key, targetIds, anchor: element, point, placement: 'top', left: 0, top: 0, arrowX: 0, measured: false }

  await nextTick()
  const state = tooltip.value
  const popup = tooltipRef.value
  if (!state || state.key !== key || !popup) return
  const rect = anchorRect(element, point)
  const width = popup.offsetWidth
  const height = popup.offsetHeight
  const x = point?.x ?? rect.left + Math.min(rect.width / 2, 16)
  const above = rect.top - height - TOOLTIP_GAP >= TOOLTIP_MARGIN
  const left = Math.max(TOOLTIP_MARGIN, Math.min(x - width / 2, window.innerWidth - width - TOOLTIP_MARGIN))
  tooltip.value = {
    ...state,
    placement: above ? 'top' : 'bottom',
    left,
    top: above ? rect.top - height - TOOLTIP_GAP : rect.bottom + TOOLTIP_GAP,
    arrowX: Math.max(12, Math.min(x - left, width - 12)),
    measured: true,
  }
}

function onPointerOver(event: MouseEvent) {
  const element = targetElement(event)
  if (element) showTooltip(element, { x: event.clientX, y: event.clientY })
  else scheduleHide()
}

function onFocusIn(event: FocusEvent) {
  const element = targetElement(event)
  if (element) showTooltip(element)
}

function onKeyDown(event: KeyboardEvent) {
  if (event.key === 'Escape') hideTooltip()
}

onMounted(() => {
  window.addEventListener('scroll', hideTooltip, true)
  window.addEventListener('resize', hideTooltip)
  window.addEventListener('keydown', onKeyDown)
})

onUnmounted(() => {
  cancelHide()
  window.removeEventListener('scroll', hideTooltip, true)
  window.removeEventListener('resize', hideTooltip)
  window.removeEventListener('keydown', onKeyDown)
})
</script>

<style scoped>
.permission-evaluation {
  font-size: 0.85rem;
  border: 0.0625rem solid var(--color-border-contrast);
  border-radius: 0.375rem;
  background-color: var(--color-background-soft);
  color: var(--color-text);
  overflow: hidden;
}

.perm-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem 0.75rem;
}

.perm-headline {
  font-weight: 600;
  color: var(--color-text-2);
}

.perm-headline-flagged {
  color: var(--color-error-text);
}

.perm-toggle {
  margin-left: auto;
  background: none;
  border: 0.0625rem solid var(--color-border-contrast);
  border-radius: 0.25rem;
  color: var(--color-link);
  padding: 0.1rem 0.5rem;
  font-size: 0.8rem;
  cursor: pointer;
  text-shadow: none;
}

.perm-toggle:hover {
  background-color: var(--color-background-hover);
}

.perm-script {
  padding: 0.75rem 0.75rem 0.75rem 0.25rem;
  background-color: var(--color-background-mute);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 0.85rem;
  line-height: 1.7;
  overflow-x: auto;
}

.perm-line {
  display: flex;
  min-height: 1.7em;
}

.perm-gutter {
  flex: none;
  width: 1.5rem;
  text-align: center;
  user-select: none;
}

.perm-gutter-icon {
  padding: 0;
  margin: 0;
  border: none;
  background: none;
  color: var(--color-error-text);
  font-family: sans-serif;
  font-weight: 700;
  font-size: 0.9em;
  line-height: inherit;
  cursor: help;
  text-shadow: none;
}

.perm-gutter-icon:focus-visible {
  outline: 0.125rem solid var(--color-error-border);
  outline-offset: 0.1rem;
  border-radius: 0.2rem;
}

.perm-line-code {
  flex: 1;
  min-width: 0;
  font: inherit;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: none;
  padding: 0;
}

.perm-unlocated-caption {
  margin: 0.5rem 0 0 1.5rem;
  font-family: system-ui, sans-serif;
  font-size: 0.75rem;
  color: var(--color-text-2);
}

.tok-command,
.tok-keyword {
  color: var(--color-link);
}

.tok-keyword {
  font-weight: 600;
}

.tok-option {
  color: var(--color-primary-hover);
}

.tok-string {
  color: var(--color-green);
}

.tok-variable {
  font-weight: 600;
}

.tok-operator,
.tok-comment {
  color: var(--color-text-2);
}

.tok-comment {
  font-style: italic;
}

.perm-flagged {
  cursor: help;
  background-color: color-mix(in srgb, var(--color-error-border) 22%, transparent);
}

.perm-flagged-depth-2 {
  background-color: color-mix(in srgb, var(--color-error-border) 34%, transparent);
}

.perm-flagged-depth-3 {
  background-color: color-mix(in srgb, var(--color-error-border) 46%, transparent);
}

.perm-details {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 0.75rem;
  border-top: 0.0625rem solid var(--color-border);
}

.perm-card {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  padding: 0.6rem 0.75rem;
  border-radius: 0.3rem;
  background-color: var(--color-background-mute);
}

.perm-card-label {
  font-weight: 600;
  color: var(--color-error-text);
}

.perm-card-command {
  font-family: monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.perm-card p {
  margin: 0;
}

.perm-muted {
  color: var(--color-text-2);
  white-space: pre-wrap;
}

.perm-deny {
  color: var(--color-error-text);
}

.perm-approved summary,
.perm-notes-title {
  cursor: pointer;
  font-weight: 600;
  color: var(--color-text-2);
  padding: 0.25rem 0;
}

.perm-notes-title {
  cursor: default;
}

.perm-approved .perm-card {
  margin-top: 0.5rem;
}

.perm-details :deep(code) {
  font-family: monospace;
}

.perm-details :deep(.perm-checks) {
  list-style: none;
  margin: 0.25rem 0 0;
  padding: 0;
  font-size: 0.8rem;
}

.perm-details :deep(.perm-checks li) {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.5rem;
  padding: 0.15rem 0.35rem;
  border-radius: 0.25rem;
  color: var(--color-text-2);
}

.perm-details :deep(.perm-check-decided) {
  background-color: var(--color-background-soft);
  color: var(--color-text);
}

.perm-details :deep(.perm-check-outcome) {
  min-width: 7.5rem;
}

.perm-details :deep(.perm-check-parts) {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.perm-details :deep(.perm-check-auto-approve) {
  color: var(--color-green);
}

.perm-details :deep(.perm-check-require-approval) {
  color: var(--color-text);
}

.perm-details :deep(.perm-check-decided-tag) {
  font-size: 0.7rem;
  padding: 0 0.35rem;
  border: 0.0625rem solid var(--color-border-contrast);
  border-radius: 0.25rem;
}

.perm-details :deep(.perm-check-detail) {
  flex-basis: 100%;
  padding-left: 8rem;
  white-space: pre-wrap;
}

/* Mirrors Tooltip.vue so tooltips look the same across the app. */
.perm-tooltip {
  position: fixed;
  z-index: 200;
  max-width: min(24rem, calc(100vw - 1rem));
  padding: 0.375rem 0.625rem;
  background: var(--color-background-mute);
  color: var(--color-text);
  border: 0.0625rem solid var(--color-border-contrast);
  border-radius: 0.375rem;
  box-shadow: 0 0.125rem 0.5rem rgba(0, 0, 0, 0.15);
  font-size: 0.8125rem;
  line-height: 1.45;
  pointer-events: none;
  overflow-wrap: anywhere;
}

.perm-tooltip::before {
  content: '';
  position: absolute;
  left: var(--arrow-x, 50%);
  width: 0.5rem;
  height: 0.5rem;
  background: var(--color-background-mute);
  border: 0.0625rem solid var(--color-border-contrast);
}

.perm-tooltip[data-placement='top']::before {
  bottom: -0.3125rem;
  transform: translateX(-50%) rotate(45deg);
  border-top: none;
  border-left: none;
}

.perm-tooltip[data-placement='bottom']::before {
  top: -0.3125rem;
  transform: translateX(-50%) rotate(45deg);
  border-bottom: none;
  border-right: none;
}

.perm-tooltip-entry + .perm-tooltip-entry {
  margin-top: 0.35rem;
  padding-top: 0.35rem;
  border-top: 0.0625rem solid var(--color-border-contrast);
}

.perm-tooltip .perm-muted {
  margin-top: 0.15rem;
}

.perm-tooltip :deep(code) {
  font-family: monospace;
}
</style>