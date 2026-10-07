def _union(xs):
    out = []
    for a, b in sorted((a, b) for a, b in xs if a < b):
        if out and a <= out[-1][1]:
            out[-1][1] = max(out[-1][1], b)
        else:
            out.append([a, b])
    return out


def solve(req):
    base, cut = _union(req["base"]), _union(req["cut"])
    out = []
    for a, b in base:
        cur = a
        for c, d in cut:
            if d <= cur or c >= b:
                continue
            if c > cur:
                out.append([cur, c])
            cur = max(cur, d)
        if cur < b:
            out.append([cur, b])
    return _union(out)
