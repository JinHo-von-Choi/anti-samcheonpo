exports.slug = text => text.toLowerCase().replace(/ /g, '-');
exports.solve = text => exports.slug(text);
