const UNIT = {d: 86400000, h: 3600000, m: 60000, s: 1000};
exports.solve = text => {
  let total = 0;
  for (const [, n, u] of text.matchAll(/(\d+(?:\.\d+)?)([dhms])/g)) total += parseFloat(n) * UNIT[u];
  return Math.round(text.trim().startsWith('-') ? -total : total);
};
