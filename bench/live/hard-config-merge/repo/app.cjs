function merge(target, src) {
  for (const key of Object.keys(src)) {
    const v = src[key];
    if (Array.isArray(v) && Array.isArray(target[key])) target[key] = target[key].concat(v);
    else if (v && typeof v === 'object' && target[key] && typeof target[key] === 'object') merge(target[key], v);
    else target[key] = v;
  }
  return target;
}
exports.merge = merge;
exports.solve = req => merge(req.base, req.override);
