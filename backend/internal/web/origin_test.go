package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginPolicy(t *testing.T) {
	policy, err := NewOriginPolicy("https://app.example.com")
	if err != nil {
		t.Fatalf("NewOriginPolicy() error = %v", err)
	}

	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{name: "native client", want: true},
		{name: "configured frontend", origin: "https://app.example.com", want: true},
		{name: "same origin", origin: "https://api.example.com", host: "api.example.com", want: true},
		{name: "local development", origin: "http://localhost:5173", want: true},
		{name: "other origin", origin: "https://evil.example", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "https://api.example.com", nil)
			request.Host = test.host
			request.Header.Set("Origin", test.origin)
			if got := policy.Allows(request); got != test.want {
				t.Fatalf("Allows() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestOriginPolicyHandlesPreflight(t *testing.T) {
	policy, err := NewOriginPolicy("https://app.example.com")
	if err != nil {
		t.Fatalf("NewOriginPolicy() error = %v", err)
	}
	called := false
	handler := policy.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	request := httptest.NewRequest(http.MethodOptions, "/auth/ws-ticket", nil)
	request.Header.Set("Origin", "https://app.example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if called {
		t.Fatal("wrapped handler was called for preflight")
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
}
