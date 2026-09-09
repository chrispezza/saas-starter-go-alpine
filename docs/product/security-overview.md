# Security Overview

This document outlines the security practices implemented in the Alpine Go Performance Starter.

## Authentication

### JWT-Based Authentication
- **Implementation**: Supabase Auth provides JWT tokens
- **Validation**: Server-side validation of JWT signatures
- **Storage**: Tokens stored in HttpOnly cookies
- **Refresh**: No sliding expiration — an expired or invalid session is logged out (both cookies cleared, redirect to `/auth/page`). The `sb-refresh-token` cookie is used exactly once, during guest-to-registered upgrade, to re-mint tokens so the `is_anonymous` claim reflects the upgrade immediately (`internal/auth/upgrade.go`)
- **Protection**: Protection against common JWT attacks (algorithm confusion, etc.)

### Configuration
```go
// Example JWT validation middleware
func JWTMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Extract JWT from Authorization header
        tokenString := extractToken(r)
        
        // Validate JWT
        token, err := validateToken(tokenString)
        if err != nil {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        
        // Set user claims in context
        ctx := context.WithValue(r.Context(), "user", token.Claims)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

## CSRF Protection

### Implementation
- Stateless double-submit cookie (ADR-014 §3): a random 32-byte token in an HttpOnly cookie, 12h max-age, reissued transparently on expiry
- Transmitted to HTMX via an `hx-headers` attribute on `<body>` (sent as the `X-CSRF-Token` header) or a hidden `csrf_token` form field
- Verified on all state-changing operations (non-GET requests)
- No per-session binding and no rotation — the submitted token is compared against the cookie in constant time

### Implementation Pattern
```go
// Generate CSRF token — random 32 bytes, stored in the double-submit cookie
func generateCSRFToken() string {
    // crypto/rand bytes, hex-encoded
    // ...
}

// Middleware to validate CSRF tokens
func CSRFProtection(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Skip validation for safe methods
        if r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
            next.ServeHTTP(w, r)
            return
        }
        
        // Get token from request header (HTMX) or form field
        requestToken := r.Header.Get("X-CSRF-Token")
        if requestToken == "" {
            requestToken = r.FormValue("csrf_token")
        }
        
        // Validate token against the double-submit cookie (constant-time compare)
        if !matchesCSRFCookie(r, requestToken) {
            http.Error(w, "Invalid CSRF token", http.StatusForbidden)
            return
        }
        
        next.ServeHTTP(w, r)
    })
}
```

## Rate Limiting

### Implementation
- Applied to authentication endpoints to prevent brute force
- Applied to API endpoints to prevent abuse
- Configurable per-endpoint with different thresholds

### Configuration
```go
// Per-IP token bucket built on golang.org/x/time/rate
// (internal/middleware/ratelimit.go), tiered via route groups (ADR-014).
// ctx is the server's lifecycle context: idle-bucket eviction stops when
// Server.Close cancels it (#117).

// Global limit: 50 req/sec, burst 10
r.Use(mw.RateLimiter(ctx, 50, 10))

// Strict tier on credential endpoints: 5 attempts per minute, burst 5
strict.Use(mw.RateLimiter(ctx, 5.0/60.0, 5))
```

## Database Security

### Row Level Security (RLS)
- Enforces user-scoped data access at the database level
- Prevents unauthorized data access even with valid queries
- Applied to all tables containing user data

### Example RLS Policy
```sql
-- Enable RLS on items table
ALTER TABLE public.items ENABLE ROW LEVEL SECURITY;

-- Create policy for users to access only their own items
CREATE POLICY user_items_policy ON public.items
    USING (user_id = auth.uid())
    WITH CHECK (user_id = auth.uid());
```

## Secure Headers

### Implementation
- Content-Security-Policy to prevent XSS
- X-Frame-Options to prevent clickjacking
- X-Content-Type-Options to prevent MIME sniffing
- Referrer-Policy to control referrer information
- Other headers as recommended by OWASP

### Configuration
```go
// Secure headers middleware
func SecureHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // script-src carries 'unsafe-eval' for Alpine expression compilation
        // (ADR-028); inline <script> stays forbidden — no 'unsafe-inline' for scripts.
        w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
        // Set to 0 per OWASP recommendation — CSP supersedes it, and non-zero
        // values can introduce XSS vulnerabilities in older browsers.
        w.Header().Set("X-XSS-Protection", "0")
        
        next.ServeHTTP(w, r)
    })
}
```

## Input Validation

### Implementation
- Server-side validation for all user inputs
- Strict type checking and sanitization
- Protection against SQL injection
- Validation integrated with HTMX for immediate feedback

### Example
```go
// Validate item creation
func validateItem(item *Item) error {
    if item.Name == "" {
        return errors.New("name is required")
    }
    
    if len(item.Name) > 100 {
        return errors.New("name must be less than 100 characters")
    }
    
    if item.Description != "" && len(item.Description) > 1000 {
        return errors.New("description must be less than 1000 characters")
    }
    
    return nil
}
```

## Additional Security Measures

- All dependencies regularly updated and scanned
- Production secrets managed through `fly secrets set` on Fly.io (ADR-015, ADR-025), not .env files
- Structured logging with sensitive data redaction
- Panic recovery middleware to prevent information disclosure
- Database connection parameters properly tuned

## Security Best Practices

- **Defense in Depth**: Multiple security layers independent of each other
- **Least Privilege**: Components access only what they need
- **Secure Defaults**: Security enabled by default, not opt-in
- **Fail Secure**: Errors default to denying access
- **Keep It Simple**: Simple security is more likely to be correct

## Further Reading

- [OWASP Top Ten](https://owasp.org/www-project-top-ten/)
- [JWT Best Practices](https://auth0.com/blog/a-look-at-the-latest-draft-for-jwt-bcp/)
- [Supabase Security Documentation](https://supabase.com/docs/guides/auth/overview#security)
- [HTMX Security Considerations](https://htmx.org/docs/#security)
