def solve(req):
    out = []
    for a, b in req["base"]:
        for c, d in req["cut"]:
            if c <= a and d >= b:
                a = b
            elif c <= a <= d:
                a = d + 1
            elif c <= b <= d:
                b = c - 1
        if a < b:
            out.append([a, b])
    return out
