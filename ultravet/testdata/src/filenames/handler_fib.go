// want package:"regfuncs"

// The sanctioned exception: fiber-native handlers live here, and only here, so
// the file name is the fleet-greppable marker for "pinned to one engine".
package filenames

type Sockets struct{}

func NewSockets() *Sockets { return &Sockets{} }
