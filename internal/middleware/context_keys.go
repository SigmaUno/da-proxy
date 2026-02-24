package middleware

// Context keys for Echo context values shared across middleware.
const (
	ContextKeyRequestID = "request_id"
	ContextKeyTokenName = "token_name"
	ContextKeyRateLimit = "rate_limit"
	ContextKeyBackend   = "backend"
	ContextKeyRPCMethod = "rpc_method"
	ContextKeyTokenInfo = "token_info"
)
