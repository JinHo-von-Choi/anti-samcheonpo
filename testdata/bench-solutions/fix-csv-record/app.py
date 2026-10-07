import csv
import io

def solve(line):
    return next(csv.reader(io.StringIO(line)))
