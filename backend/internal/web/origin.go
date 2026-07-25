package web

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type OriginPolicy struct {
	allowed map[string]struct{}
}

func NewOriginPolicy(allowedList string) (OriginPolicy, error) {
	policy := OriginPolicy{allowed: make(map[string]struct{})}
	allowedList = strings.TrimSpace(allowedList)
	if allowedList == "" {
		return policy, nil
	}

	for _, allowed := range strings.Split(allowedList, ",") {
		normalized, err := normalizeOrigin(allowed)
		if err != nil {
			return OriginPolicy{}, errors.New("invalid FRONTEND_ORIGIN")
		}
		policy.allowed[strings.ToLower(normalized)] = struct{}{}
	}
	return policy, nil
}

func (p OriginPolicy) Allows(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	normalized, err := normalizeOrigin(origin)
	if err != nil {
		return false
	}
	parsed, _ := url.Parse(normalized)
	_, configured := p.allowed[strings.ToLower(normalized)]

	return configured ||
		strings.EqualFold(parsed.Host, r.Host) ||
		isLocalhost(parsed.Hostname())
}

func (p OriginPolicy) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !p.Allows(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}

		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func normalizeOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("invalid origin")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func isLocalhost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
