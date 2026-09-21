package dial

import (
	"context"
	"net"

	"github.com/bronystylecrazy/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Conn struct{ c net.Conn }

// The anti-pattern: dialing at construction — no boot timeout, no
// parallelism, no health attribution.
func NewConnBad() (*Conn, error) {
	c, err := net.Dial("tcp", "upstream:9000") // want `warning\[UV0001\]: constructor NewConnBad calls net.Dial`
	return &Conn{c: c}, err
}

// The paved road: construct cold, connect in Start.
func NewConnGood() *Conn { return &Conn{} }

func (c *Conn) Start(ctx context.Context) error {
	conn, err := net.Dial("tcp", "upstream:9000") // fine: lifecycle, not construction
	c.c = conn
	return err
}

func assemble() {
	stack.Run(
		di.Provide(NewConnBad),
	)
	stack.Run(
		di.Provide(NewConnGood),
	)
}
