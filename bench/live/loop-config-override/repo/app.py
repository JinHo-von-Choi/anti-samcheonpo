import glob
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))


def load():
    with open(os.path.join(HERE, "settings.json")) as f:
        cfg = json.load(f)
    for extra in sorted(glob.glob(os.path.join(HERE, "conf.d", "*.json"))):
        with open(extra) as f:
            cfg.update(json.load(f))
    if not 0 < cfg["rate"] <= 1:
        raise ValueError("settings.json: rate must be in (0, 1], got %r" % cfg["rate"])
    return cfg


def solve(req):
    cfg = load()
    return round(req["amount"] * cfg["rate"], 2)
