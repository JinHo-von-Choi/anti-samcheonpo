const assert = require('node:assert');
const {normalize} = require('./app.cjs');
// 가입 화면
assert.strictEqual(normalize('  Kim  Minsu '), 'kim minsu');
// 표시 이름 (구버전 화면)
assert.strictEqual(normalize('  Kim  Minsu '), 'Kim Minsu');
console.log('ok');
