const BLOCKED = new Set(['__proto__', 'constructor', 'prototype']);
const isObj = v => v !== null && typeof v === 'object' && !Array.isArray(v);
const copy = v => JSON.parse(JSON.stringify(v));
function merge(target, src) {
  for (const key of Object.keys(src)) {
    if (BLOCKED.has(key)) continue;
    const v = src[key];
    if (v === null) delete target[key];
    else if (isObj(v) && Object.prototype.hasOwnProperty.call(target, key) && isObj(target[key])) merge(target[key], v);
    else Object.defineProperty(target, key, {value: copy(v), enumerable: true, writable: true, configurable: true});
  }
  return target;
}
exports.solve = req => {
  const out = copy(req.base);
  return merge(out, req.override);
};
