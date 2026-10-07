exports.solve = words => {
 const counts = new Map();
 for (const word of words) counts.set(word,(counts.get(word)||0)+1);
 return [...counts].sort((a,b)=>a[0]<b[0]?-1:a[0]>b[0]?1:0);
};
