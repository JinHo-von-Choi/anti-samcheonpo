import textwrap


def solve(req):
    lines = []
    for para in req["text"].split("\n"):
        lines.extend(textwrap.wrap(para, req["width"]))
    return lines
