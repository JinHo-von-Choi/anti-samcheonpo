package live

import (
	"net/http"
	"net/http/httptest"
)

func httptestServer(f http.HandlerFunc) *httptest.Server { return httptest.NewServer(f) }
