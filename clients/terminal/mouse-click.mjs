// Adapted from Orca, Copyright (c) 2026 Lovecast Inc., MIT.
// Source revision, unchanged references, and license: third_party/orca/.
const ESC = '\x1b'

/**
 * Encode a click only after tracking and encoding have been observed.
 * Coordinates come from the client's viewport adapter, not screen pixels.
 * @param {{tracking: 'none'|'x10'|'vt200'|'drag'|'any', encoding: 'unknown'|'sgr'|'pixel'|'legacy', col: number, row: number, x?: number, y?: number}} input
 * @returns {string}
 */
export function encodeMouseClick(input) {
  const { tracking, encoding, col, row } = input
  if (tracking === 'none' || encoding === 'unknown') return ''
  if (encoding !== 'pixel' && (!Number.isInteger(col) || !Number.isInteger(row) || col < 0 || row < 0)) return ''
  if (encoding === 'sgr' || encoding === 'pixel') {
    const x = encoding === 'pixel' ? input.x : col + 1
    const y = encoding === 'pixel' ? input.y : row + 1
    if (!Number.isInteger(x) || !Number.isInteger(y) || x < 0 || y < 0 || x > 9999 || y > 9999) return ''
    const press = `${ESC}[<0;${x};${y}M`
    return tracking === 'x10' ? press : press + `${ESC}[<0;${x};${y}m`
  }
  const encodedCol = col + 33
  const encodedRow = row + 33
  if (!Number.isInteger(col) || !Number.isInteger(row) || col < 0 || row < 0 || encodedCol > 126 || encodedRow > 126) return ''
  const press = ESC + '[M' + String.fromCharCode(32, encodedCol, encodedRow)
  return tracking === 'x10' ? press : press + ESC + '[M' + String.fromCharCode(35, encodedCol, encodedRow)
}
