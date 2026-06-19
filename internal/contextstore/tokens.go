package contextstore

// EstimateTokens approximates token count at ~4 characters per token.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}
