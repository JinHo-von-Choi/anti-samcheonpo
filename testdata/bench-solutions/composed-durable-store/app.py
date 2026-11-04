import json
import os
from pathlib import Path
import tempfile


def solve(request):
    operation = request.get("op")
    if operation not in {"put", "get", "delete"}:
        return {"error": "operation"}
    key = request.get("key")
    if not isinstance(key, str) or not key:
        return {"error": "invalid"}
    if operation == "put" and type(request.get("value")) is not int:
        return {"error": "invalid"}
    path = Path(request["path"])
    data = json.loads(path.read_text()) if path.exists() else {}
    if operation == "get":
        return {"value": data.get(key)}
    present = key in data
    if operation == "put":
        if present:
            return {"inserted": False}
        data[key] = request["value"]
        result = {"inserted": True}
    else:
        if not present:
            return {"deleted": False}
        del data[key]
        result = {"deleted": True}
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(mode="w", dir=path.parent, delete=False) as output:
        json.dump(data, output)
        temporary = output.name
    os.replace(temporary, path)
    return result
