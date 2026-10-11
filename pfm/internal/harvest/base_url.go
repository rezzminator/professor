package harvest

import "context"

type baseURLKey struct{}

// withBaseURL carries the address a page was finally fetched from — after
// every redirect — to the converter (BaseURLFrom). "" carries nothing.
func withBaseURL(ctx context.Context, address string) context.Context {
	if address == "" {
		return ctx
	}
	return context.WithValue(ctx, baseURLKey{}, address)
}

// BaseURLFrom is the address the document being converted was fetched from,
// the base its relative links and image sources resolve against; "" when the
// fetch reported none (the converter then falls back to the source it was
// asked for).
func BaseURLFrom(ctx context.Context) string {
	address, _ := ctx.Value(baseURLKey{}).(string)
	return address
}
