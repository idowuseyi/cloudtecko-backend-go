package auth

type Scope string

const (
	ScopeApp Scope = "app"
	// ScopeAdmin guards /api/v1/admin/*. Note that CheckUserScopes is OR, so a
	// route group requiring admin must list this scope alone -- pairing it with
	// ScopeApp would admit every authenticated user.
	ScopeAdmin Scope = "admin"
)

func (s Scope) String() string {
	return string(s)
}
