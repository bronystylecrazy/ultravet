package provideopts

import (
	"github.com/bronystylecrazy/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type OrderRepo interface{ Find() string }
type PgRepo struct{}

func (*PgRepo) Find() string { return "" }

func NewPgRepo() *PgRepo { return &PgRepo{} }

type Flags struct{}

func NewFlags() *Flags { return &Flags{} }

type OrderService struct{ repo OrderRepo }

func NewOrderService(repo OrderRepo) *OrderService { return &OrderService{repo: repo} }

type FlagUser struct{ f *Flags }

func NewFlagUser(f *Flags) *FlagUser { return &FlagUser{f: f} }

// di.As[Iface]() interleaved with the constructor is a ProvideOption, not a
// bad argument: no DI0010, and the facet interface is registered so the
// consumer of OrderRepo resolves.
func asOptionResolves() {
	stack.Run(
		di.Provide(NewPgRepo, di.As[OrderRepo]()),
		di.Provide(NewOrderService),
	)
}

// di.NonCritical is a ProvideOption value: no DI0010, and the consumer of the
// concrete *Flags still resolves.
func nonCriticalOptionResolves() {
	stack.Run(
		di.Provide(NewFlags, di.NonCritical),
		di.Provide(NewFlagUser),
	)
}

// A genuinely bad argument — a non-constructor, non-option value — is still
// flagged DI0010: option-skipping does not silence real shape errors.
func badArgStillFlagged() {
	stack.Run(
		di.Provide(NewFlags, "not a constructor"), // want `error\[DI0010\]: di.Provide takes constructor functions, got string`
	)
}
