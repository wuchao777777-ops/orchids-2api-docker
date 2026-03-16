package middleware

import "net/http"

// SecurityHeaders adds common security headers to all responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-XSS-Protection", "0")
		// Allow Grok Voice to request microphone access on this origin while
		// keeping other powerful features denied by default.
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(self), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
