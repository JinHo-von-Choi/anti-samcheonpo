"""Implement the durable store contract in task.yml."""
values = {}


def solve(request):
    key = request.get("key")
    if request.get("op") == "put":
        values[key] = request.get("value")
        return {"inserted": True}
    if request.get("op") == "delete":
        return {"deleted": values.pop(key, None) is not None}
    return {"value": values.get(key)}
