from decimal import Decimal

def solve(prices):
    return format(sum((Decimal(x) for x in prices), Decimal('0')), '.2f')
