import heapq


def solve(req):
    nodes = list(req["nodes"])
    edges = {(a, b) for a, b in req["edges"]}
    nxt = {n: set() for n in nodes}
    indeg = {n: 0 for n in nodes}
    for a, b in edges:
        nxt[a].add(b)
        indeg[b] += 1
    heap = [n for n in nodes if indeg[n] == 0]
    heapq.heapify(heap)
    order = []
    while heap:
        n = heapq.heappop(heap)
        order.append(n)
        for m in nxt[n]:
            indeg[m] -= 1
            if indeg[m] == 0:
                heapq.heappush(heap, m)
    if len(order) == len(nodes):
        return {"order": order}
    index, low, stack, on, counter, cyc = {}, {}, [], set(), [0], set()

    def visit(v):
        index[v] = low[v] = counter[0]
        counter[0] += 1
        stack.append(v)
        on.add(v)
        for w in nxt[v]:
            if w not in index:
                visit(w)
                low[v] = min(low[v], low[w])
            elif w in on:
                low[v] = min(low[v], index[w])
        if low[v] == index[v]:
            comp = []
            while True:
                w = stack.pop()
                on.discard(w)
                comp.append(w)
                if w == v:
                    break
            if len(comp) > 1 or v in nxt[v]:
                cyc.update(comp)

    for n in nodes:
        if n not in index:
            visit(n)
    return {"cycle": sorted(cyc)}
