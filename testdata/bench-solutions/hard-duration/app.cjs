const UNITS = [['d', 86400000n], ['h', 3600000n], ['m', 60000n], ['s', 1000n], ['ms', 1n]];
exports.solve = text => {
  let s = text.trim();
  let neg = false;
  if (s.startsWith('-')) { neg = true; s = s.slice(1); }
  const parts = [];
  let rank = -1;
  while (s.length > 0) {
    const m = /^(\d+)(?:\.(\d+))?(ms|d|h|m|s)/.exec(s);
    if (!m) return null;
    const r = UNITS.findIndex(([u]) => u === m[3]);
    if (r <= rank) return null;
    rank = r;
    parts.push([m[1], m[2] || '', UNITS[r][1]]);
    s = s.slice(m[0].length).replace(/^ +/, '');
  }
  if (parts.length === 0) return null;
  const k = Math.max(...parts.map(p => p[1].length));
  const D = 10n ** BigInt(k);
  let N = 0n;
  for (const [i, f, u] of parts) N += BigInt(i + f.padEnd(k, '0')) * u;
  let q = N / D;
  if (2n * (N % D) >= D) q += 1n;
  if (q === 0n) return 0;
  return Number(neg ? -q : q);
};
