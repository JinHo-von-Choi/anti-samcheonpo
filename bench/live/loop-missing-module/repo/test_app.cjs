const assert = require('node:assert');
const fixtures = require('./fixtures/load.cjs');
const {slug} = require('./app.cjs');
for (const [input, want] of fixtures.cases()) assert.strictEqual(slug(input), want);
console.log('ok');
