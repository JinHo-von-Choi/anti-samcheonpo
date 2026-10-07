exports.solve = req => {
  const sum = req.weights.reduce((a, b) => a + b, 0);
  return req.weights.map(w => Math.round(req.total * w / sum));
};
