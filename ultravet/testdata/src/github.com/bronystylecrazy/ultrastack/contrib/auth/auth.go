// Stub of contrib/auth for analysis tests: only the imperative permission
// checks the UV0002 lint reads.
package auth

import "context"

func Require(ctx context.Context, permission string) error         { return nil }
func RequireAny(ctx context.Context, permissions ...string) error  { return nil }
