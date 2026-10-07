def solve(request):
    start = request['page'] * request['size']
    return request['items'][start:start + request['size']]
