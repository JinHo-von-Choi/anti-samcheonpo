exports.slug = text => text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
exports.solve = text => exports.slug(text);
