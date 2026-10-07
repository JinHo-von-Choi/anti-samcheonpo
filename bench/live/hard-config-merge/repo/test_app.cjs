const assert = require('node:assert');
const {solve} = require('./app.cjs');
assert.deepStrictEqual(solve({base: {a: 1, b: {c: 2}}, override: {b: {d: 3}}}), {a: 1, b: {c: 2, d: 3}});
assert.deepStrictEqual(solve({base: {plugins: ['lint']}, override: {plugins: ['fmt']}}), {plugins: ['lint', 'fmt']});
console.log('ok');
