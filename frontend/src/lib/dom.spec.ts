import { describe, it, expect } from 'vitest'
import { isInteractiveElement } from './dom'

describe('isInteractiveElement', () => {
  it('returns false for null', () => {
    expect(isInteractiveElement(null)).toBe(false)
  })

  it.each(['INPUT', 'TEXTAREA', 'SELECT'])('returns true for %s elements', (tag) => {
    const el = document.createElement(tag)
    expect(isInteractiveElement(el)).toBe(true)
  })

  it('returns true for contenteditable elements', () => {
    const el = document.createElement('div')
    el.setAttribute('contenteditable', 'true')
    expect(isInteractiveElement(el)).toBe(true)
  })

  it.each([
    'textbox', 'combobox', 'searchbox', 'spinbutton',
    'listbox', 'menu', 'menubar', 'tree', 'treegrid', 'grid',
    'tablist', 'dialog',
  ])('returns true for role="%s"', (role) => {
    const el = document.createElement('div')
    el.setAttribute('role', role)
    expect(isInteractiveElement(el)).toBe(true)
  })

  it.each(['combobox', 'listbox', 'menu', 'dialog'])(
    'returns true for a child inside role="%s"',
    (role) => {
      const parent = document.createElement('div')
      parent.setAttribute('role', role)
      const child = document.createElement('span')
      parent.appendChild(child)
      document.body.appendChild(parent)
      expect(isInteractiveElement(child)).toBe(true)
      document.body.removeChild(parent)
    }
  )

  it('returns false for a plain div', () => {
    const el = document.createElement('div')
    expect(isInteractiveElement(el)).toBe(false)
  })

  it('returns false for a button', () => {
    const el = document.createElement('button')
    expect(isInteractiveElement(el)).toBe(false)
  })
})