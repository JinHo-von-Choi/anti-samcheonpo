const assert = require('node:assert');
const {solve} = require('./app.cjs');
assert.deepStrictEqual(solve({total: 100, weights: [1, 1]}), [50, 50]);
console.log('ok');
