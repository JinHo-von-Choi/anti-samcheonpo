from collections import deque


def solve(req):
    indeg = {n: 0 for n in req["nodes"]}
    nxt = {n: [] for n in req["nodes"]}
    for a, b in req["edges"]:
        nxt[a].append(b)
        indeg[b] += 1
    q = deque(n for n in req["nodes"] if indeg[n] == 0)
    order = []
    while q:
        n = q.popleft()
        order.append(n)
        for m in nxt[n]:
            indeg[m] -= 1
            if indeg[m] == 0:
                q.append(m)
    if len(order) < len(req["nodes"]):
        return {"cycle": sorted(n for n in req["nodes"] if n not in order)}
    return {"order": order}
