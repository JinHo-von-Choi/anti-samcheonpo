exports.solve = ({query,key}) => query.split('&').filter(x=>x.startsWith(key+'=')).map(x=>x.split('=')[1]);
