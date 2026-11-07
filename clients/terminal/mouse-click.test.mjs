import assert from 'node:assert/strict'
import { test } from 'node:test'
import { encodeMouseClick } from './mouse-click.mjs'

test('SGR click uses one-based cells and includes release', () => {
  assert.equal(encodeMouseClick({ tracking: 'vt200', encoding: 'sgr', col: 37, row: 19 }), '\x1b[<0;38;20M\x1b[<0;38;20m')
})

test('X10 emits press only', () => {
  assert.equal(encodeMouseClick({ tracking: 'x10', encoding: 'sgr', col: 0, row: 0 }), '\x1b[<0;1;1M')
})

test('pixel encoding uses zero-based pixels instead of cell coordinates', () => {
  assert.equal(encodeMouseClick({ tracking: 'any', encoding: 'pixel', col: 8, row: 9, x: 0, y: 240 }), '\x1b[<0;0;240M\x1b[<0;0;240m')
})

test('unknown encoding or disabled tracking emits no guessed input', () => {
  assert.equal(encodeMouseClick({ tracking: 'vt200', encoding: 'unknown', col: 0, row: 0 }), '')
  assert.equal(encodeMouseClick({ tracking: 'none', encoding: 'sgr', col: 0, row: 0 }), '')
})

test('legacy encoding stays ASCII and uses button 35 for release', () => {
  assert.equal(encodeMouseClick({ tracking: 'drag', encoding: 'legacy', col: 0, row: 0 }), '\x1b[M !!\x1b[M#!!')
  assert.equal(encodeMouseClick({ tracking: 'x10', encoding: 'legacy', col: 0, row: 0 }), '\x1b[M !!')
  assert.equal(encodeMouseClick({ tracking: 'any', encoding: 'legacy', col: 94, row: 0 }), '')
})

test('unrepresentable coordinates emit no input', () => {
  for (const col of [-1, 9999, Infinity, NaN, 1.5]) {
    assert.equal(encodeMouseClick({ tracking: 'vt200', encoding: 'sgr', col, row: 0 }), '')
  }
  assert.equal(encodeMouseClick({ tracking: 'any', encoding: 'pixel', col: 0, row: 0, x: 10000, y: 0 }), '')
})
