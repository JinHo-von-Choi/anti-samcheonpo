"""Reject tracked filenames that collide on case-insensitive file systems."""
import subprocess
import unicodedata

paths = subprocess.check_output(["git", "ls-files", "-z"]).decode().split("\0")
seen = {}
collisions = []
for path in filter(None, paths):
    key = unicodedata.normalize("NFC", path).casefold()
    if key in seen:
        collisions.append((seen[key], path))
    seen[key] = path
if collisions:
    for first, second in collisions:
        print(f"Filename collision: {first} / {second}")
    raise SystemExit(1)
print("Tracked filenames are distinct on case-insensitive file systems.")
