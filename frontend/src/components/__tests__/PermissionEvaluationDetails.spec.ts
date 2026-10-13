import { expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PermissionEvaluationDetails from '../PermissionEvaluationDetails.vue'
import type { ScriptPermissionEvaluation } from '../../lib/models'

it('highlights a command before its redirection without duplicating it in the fallback section', () => {
  const command = 'git checkout -q main'
  const script = `cd .side/tmp/builderhub && ${command} 2>/dev/null; git status`
  const evaluation: ScriptPermissionEvaluation = {
    outcome: 'require_approval',
    commands: [{
      command,
      outcome: 'require_approval',
      factors: [{ kind: 'no_rule_matched', outcome: 'require_approval' }],
      decidedBy: 'factor',
      decidedByIndex: 0,
    }],
  }
  const wrapper = mount(PermissionEvaluationDetails, { props: { script, evaluation } })

  try {
    expect(wrapper.find('.perm-headline').text()).toBe('1 item needs approval')
    expect(wrapper.find('.perm-unlocated-caption').exists()).toBe(false)
    expect(wrapper.findAll('.perm-flagged').map((segment) => segment.element.textContent).join('')).toBe(command)
    expect(wrapper.findAll('.perm-line-code').map((line) => line.element.textContent).join('\n')).toBe(script)
  } finally {
    wrapper.unmount()
  }
})