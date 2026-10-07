exports.normalize = name => name.trim().replace(/\s+/g, ' ').toLowerCase();
exports.solve = name => exports.normalize(name);
