// Minimal QR Code encoder (byte mode, versions 1-40, error correction L or M), after the public-domain structure of
// Nayuki's reference design. Used for the UPI payment QR and the signed e-invoice QR; no dependency.
// shortcut: byte mode only (UTF-8), no ECI, no Kanji/numeric packing: a QR a few percent larger than optimal.

const ECC_PER_BLOCK = {
  L: [
    -1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28,
    30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30,
  ],
  M: [
    -1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28,
    28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28,
  ],
}
const BLOCKS = {
  L: [
    -1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16,
    17, 18, 19, 19, 20, 21, 22, 24, 25,
  ],
  M: [
    -1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28,
    29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49,
  ],
}
const FORMAT_BITS = { L: 1, M: 0 }

function rawModules(v: number): number {
  let r = (16 * v + 128) * v + 64
  if (v >= 2) {
    const a = Math.floor(v / 7) + 2
    r -= (25 * a - 10) * a - 55
    if (v >= 7) r -= 36
  }
  return r
}
const dataCodewords = (v: number, e: 'L' | 'M') =>
  Math.floor(rawModules(v) / 8) - ECC_PER_BLOCK[e][v] * BLOCKS[e][v]

function rsDivisor(degree: number): number[] {
  const r = new Array(degree).fill(0)
  r[degree - 1] = 1
  let root = 1
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < degree; j++) {
      r[j] = mul(r[j], root)
      if (j + 1 < degree) r[j] ^= r[j + 1]
    }
    root = mul(root, 2)
  }
  return r
}
function mul(x: number, y: number): number {
  let z = 0
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d)
    z ^= ((y >>> i) & 1) * x
  }
  return z
}
function rsRemainder(data: number[], div: number[]): number[] {
  const r = div.map(() => 0)
  for (const b of data) {
    const f = b ^ (r.shift() as number)
    r.push(0)
    div.forEach((c, i) => {
      r[i] ^= mul(c, f)
    })
  }
  return r
}

/** Returns the module matrix (true = dark) without the quiet zone. Throws if the text is too long. */
export function qrMatrix(text: string, ecc: 'L' | 'M' = 'M'): boolean[][] {
  const bytes = Array.from(new TextEncoder().encode(text))
  let v = 1
  for (; ; v++) {
    if (v > 40) throw new Error('QR data too long')
    const bits = 4 + (v < 10 ? 8 : 16) + bytes.length * 8
    if (bits <= dataCodewords(v, ecc) * 8) break
  }
  const bb: number[] = []
  const put = (val: number, n: number) => {
    for (let i = n - 1; i >= 0; i--) bb.push((val >>> i) & 1)
  }
  put(4, 4)
  put(bytes.length, v < 10 ? 8 : 16)
  for (const b of bytes) put(b, 8)
  const cap = dataCodewords(v, ecc) * 8
  put(0, Math.min(4, cap - bb.length))
  while (bb.length % 8) bb.push(0)
  for (let pad = 0xec; bb.length < cap; pad ^= 0xec ^ 0x11) put(pad, 8)
  const data: number[] = []
  for (let i = 0; i < bb.length; i += 8) data.push(parseInt(bb.slice(i, i + 8).join(''), 2))

  // split into blocks, add Reed-Solomon, interleave
  const nb = BLOCKS[ecc][v]
  const eccLen = ECC_PER_BLOCK[ecc][v]
  const raw = Math.floor(rawModules(v) / 8)
  const shortBlocks = nb - (raw % nb)
  const shortLen = Math.floor(raw / nb)
  const div = rsDivisor(eccLen)
  const blocks: number[][] = []
  for (let i = 0, k = 0; i < nb; i++) {
    const len = shortLen - eccLen + (i < shortBlocks ? 0 : 1)
    const d = data.slice(k, k + len)
    k += len
    const e = rsRemainder(d, div)
    if (i < shortBlocks) d.push(0) // placeholder keeps columns aligned; skipped below
    blocks.push(d.concat(e))
  }
  const all: number[] = []
  for (let i = 0; i < blocks[0].length; i++)
    blocks.forEach((b, j) => {
      if (i !== shortLen - eccLen || j >= shortBlocks) all.push(b[i])
    })

  // function patterns
  const size = v * 4 + 17
  const m: boolean[][] = Array.from({ length: size }, () => new Array(size).fill(false))
  const fn: boolean[][] = Array.from({ length: size }, () => new Array(size).fill(false))
  const set = (x: number, y: number, dark: boolean) => {
    m[y][x] = dark
    fn[y][x] = true
  }
  for (let i = 0; i < size; i++) {
    set(6, i, i % 2 === 0)
    set(i, 6, i % 2 === 0)
  }
  const finder = (cx: number, cy: number) => {
    for (let dy = -4; dy <= 4; dy++)
      for (let dx = -4; dx <= 4; dx++) {
        const d = Math.max(Math.abs(dx), Math.abs(dy))
        const x = cx + dx
        const y = cy + dy
        if (x >= 0 && x < size && y >= 0 && y < size) set(x, y, d !== 2 && d !== 4)
      }
  }
  finder(3, 3)
  finder(size - 4, 3)
  finder(3, size - 4)
  if (v > 1) {
    const n = Math.floor(v / 7) + 2
    const step = v === 32 ? 26 : Math.ceil((v * 4 + 4) / (n * 2 - 2)) * 2
    const pos = [6]
    for (let p = size - 7; pos.length < n; p -= step) pos.splice(1, 0, p)
    for (let i = 0; i < n; i++)
      for (let j = 0; j < n; j++) {
        if ((i === 0 && j === 0) || (i === 0 && j === n - 1) || (i === n - 1 && j === 0)) continue
        for (let dy = -2; dy <= 2; dy++)
          for (let dx = -2; dx <= 2; dx++)
            set(pos[i] + dx, pos[j] + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1)
      }
  }
  const drawFormat = (mask: number) => {
    const d = (FORMAT_BITS[ecc] << 3) | mask
    let r = d
    for (let i = 0; i < 10; i++) r = (r << 1) ^ ((r >>> 9) * 0x537)
    const bits = ((d << 10) | r) ^ 0x5412
    const b = (i: number) => ((bits >>> i) & 1) !== 0
    for (let i = 0; i <= 5; i++) set(8, i, b(i))
    set(8, 7, b(6))
    set(8, 8, b(7))
    set(7, 8, b(8))
    for (let i = 9; i < 15; i++) set(14 - i, 8, b(i))
    for (let i = 0; i < 8; i++) set(size - 1 - i, 8, b(i))
    for (let i = 8; i < 15; i++) set(8, size - 15 + i, b(i))
    set(8, size - 8, true)
  }
  drawFormat(0)
  if (v >= 7) {
    let r = v
    for (let i = 0; i < 12; i++) r = (r << 1) ^ ((r >>> 11) * 0x1f25)
    const bits = (v << 12) | r
    for (let i = 0; i < 18; i++) {
      const bit = ((bits >>> i) & 1) !== 0
      const a = size - 11 + (i % 3)
      const b = Math.floor(i / 3)
      set(a, b, bit)
      set(b, a, bit)
    }
  }

  // place data in the zigzag
  let i = 0
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5
    for (let vert = 0; vert < size; vert++)
      for (let j = 0; j < 2; j++) {
        const x = right - j
        const up = ((right + 1) & 2) === 0
        const y = up ? size - 1 - vert : vert
        if (!fn[y][x] && i < all.length * 8) {
          m[y][x] = ((all[i >>> 3] >>> (7 - (i & 7))) & 1) !== 0
          i++
        }
      }
  }

  const maskFn = [
    (x: number, y: number) => (x + y) % 2 === 0,
    (_x: number, y: number) => y % 2 === 0,
    (x: number) => x % 3 === 0,
    (x: number, y: number) => (x + y) % 3 === 0,
    (x: number, y: number) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0,
    (x: number, y: number) => ((x * y) % 2) + ((x * y) % 3) === 0,
    (x: number, y: number) => (((x * y) % 2) + ((x * y) % 3)) % 2 === 0,
    (x: number, y: number) => (((x + y) % 2) + ((x * y) % 3)) % 2 === 0,
  ]
  const apply = (k: number) => {
    for (let y = 0; y < size; y++)
      for (let x = 0; x < size; x++) if (!fn[y][x] && maskFn[k](x, y)) m[y][x] = !m[y][x]
  }
  let best = 0
  let bestScore = Number.POSITIVE_INFINITY
  for (let k = 0; k < 8; k++) {
    apply(k)
    drawFormat(k)
    const s = penalty(m)
    if (s < bestScore) {
      bestScore = s
      best = k
    }
    apply(k) // undo
  }
  apply(best)
  drawFormat(best)
  return m
}

/** Penalty rules N1-N4 of the specification, used only to pick the mask. */
function penalty(m: boolean[][]): number {
  const n = m.length
  let score = 0
  const runs = (get: (a: number, b: number) => boolean) => {
    for (let a = 0; a < n; a++) {
      let run = 1
      for (let b = 1; b < n; b++) {
        if (get(a, b) === get(a, b - 1)) {
          run++
          if (run === 5) score += 3
          else if (run > 5) score++
        } else run = 1
      }
      // finder-like 1:1:3:1:1 pattern with 4 light modules on either side
      const line = Array.from({ length: n }, (_, b) => (get(a, b) ? '1' : '0')).join('')
      for (const pat of ['10111010000', '00001011101']) {
        let at = line.indexOf(pat)
        while (at >= 0) {
          score += 40
          at = line.indexOf(pat, at + 1)
        }
      }
    }
  }
  runs((a, b) => m[a][b])
  runs((a, b) => m[b][a])
  for (let y = 0; y < n - 1; y++)
    for (let x = 0; x < n - 1; x++)
      if (m[y][x] === m[y][x + 1] && m[y][x] === m[y + 1][x] && m[y][x] === m[y + 1][x + 1]) score += 3
  const dark = m.flat().filter(Boolean).length
  score += Math.floor(Math.abs(dark * 20 - n * n * 10) / (n * n)) * 10
  return score
}

/** An SVG path string for the matrix (one rect per dark module, merged per row), with a 4-module quiet zone. */
export function qrPath(m: boolean[][]): { d: string; size: number } {
  const q = 4
  let d = ''
  m.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) {
      if (!row[x]) continue
      let w = 1
      while (x + w < row.length && row[x + w]) w++
      d += `M${x + q},${y + q}h${w}v1h${-w}z`
      x += w - 1
    }
  })
  return { d, size: m.length + q * 2 }
}
