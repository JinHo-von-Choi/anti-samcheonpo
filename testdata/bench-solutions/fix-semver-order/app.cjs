exports.solve = versions => [...versions].sort((a,b) => {
 const x=a.split('.').map(Number), y=b.split('.').map(Number);
 return x[0]-y[0] || x[1]-y[1] || x[2]-y[2];
});
