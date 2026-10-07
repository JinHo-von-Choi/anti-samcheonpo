const assert = require('node:assert');
const {solve} = require('./app.cjs');
assert.strictEqual(solve('1h30m'), 5400000);
console.log('ok');
