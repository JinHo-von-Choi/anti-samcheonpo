def solve(lines):
    out = {}
    for line in lines:
        code = line.split(" ")[1]
        out[code] = out.get(code, 0) + 1
    return out
