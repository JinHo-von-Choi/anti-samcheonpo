exports.solve = ({query,key}) => new URLSearchParams(query).getAll(key);
