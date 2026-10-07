import unicodedata


def _w(c):
    if unicodedata.combining(c):
        return 0
    return 2 if unicodedata.east_asian_width(c) in ("W", "F") else 1


def solve(req):
    width = req["width"]
    out = []
    for para in req["text"].split("\n"):
        words = [w for w in para.split(" ") if w]
        if not words:
            out.append("")
            continue
        cur, cw = "", 0
        for word in words:
            ww = sum(_w(c) for c in word)
            if ww <= width:
                need = ww if not cur else cw + 1 + ww
                if need <= width:
                    cur, cw = (word, ww) if not cur else (cur + " " + word, need)
                else:
                    out.append(cur)
                    cur, cw = word, ww
                continue
            if cur:
                out.append(cur)
            cur, cw = "", 0
            for c in word:
                x = _w(c)
                if cw + x > width:
                    out.append(cur)
                    cur, cw = "", 0
                cur += c
                cw += x
        if cur:
            out.append(cur)
    return out
