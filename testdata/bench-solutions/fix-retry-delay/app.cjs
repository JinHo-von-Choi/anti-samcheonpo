exports.solve = ({attempt,base,cap}) => Math.min(cap,base*2**Math.max(0,attempt));
