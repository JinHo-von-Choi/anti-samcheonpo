exports.solve = req => {
  const ws = req.weights;
  const W = ws.reduce((a, b) => a + b, 0);
  if (ws.length === 0 || W === 0) return null;
  const sign = req.total < 0 ? -1 : 1;
  const T = Math.abs(req.total);
  const base = ws.map(w => Math.floor(T * w / W));
  let r = T - base.reduce((a, b) => a + b, 0);
  const order = ws.map((w, i) => [(T * w) % W, i]).sort((a, b) => b[0] - a[0] || a[1] - b[1]);
  for (let k = 0; k < r; k++) base[order[k][1]] += 1;
  return base.map(x => (x === 0 ? 0 : sign * x));
};
