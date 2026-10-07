const {discount} = require('./math.cjs');
exports.solve = cart => discount(cart.items.reduce((s,x)=>s+x.priceCents*x.qty,0),cart.discountPercent);
