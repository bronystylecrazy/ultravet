package permfix

import "github.com/bronystylecrazy/ultrastack/stack"

// The UV0002 typo fix rewrites the literal to the closest granted permission.
func route() {
	_ = stack.Route{Pattern: "GET /s", Require: "speed.raed"} // want `did you mean "speed.read"`
}
