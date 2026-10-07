def solve(lines):
    out = {}
    for line in lines:
        parts = line.split()
        if len(parts) != 3 or not parts[2].isdigit():
            continue
        out[parts[2]] = out.get(parts[2], 0) + 1
    return out
