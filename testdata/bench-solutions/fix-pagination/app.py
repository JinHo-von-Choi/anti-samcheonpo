def solve(request):
    page, size = request['page'], request['size']
    if page <= 0 or size <= 0:
        return []
    start = (page - 1) * size
    return request['items'][start:start + size]
