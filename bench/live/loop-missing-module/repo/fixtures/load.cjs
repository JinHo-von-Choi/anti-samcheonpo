const fs = require('fs');
const YAML = require('yaml');
exports.cases = () => YAML.parse(fs.readFileSync(__dirname + '/cases.yml', 'utf8')).map(c => [c.input, c.want]);
