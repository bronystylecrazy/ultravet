// Package defaults pins di.Default's back-off: a default provider is
// auto-configuration that steps aside when a real one exists, so it must not
// count toward the ambiguity check. Every expectation here was verified
// against the runtime (di.Validate / stack.Validate) — the analyzer's job is
// to say the same thing earlier, never something different.
package defaults

import (
	"log/slog"

	"github.com/bronystylecrazy/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Store interface{ Get() string }

type MemStore struct{}

func (*MemStore) Get() string { return "" }

type PgStore struct{}

func (*PgStore) Get() string { return "" }

func NewMem() *MemStore { return &MemStore{} }
func NewPg() *PgStore   { return &PgStore{} }

type Handler struct{}

func NewHandler(s Store) *Handler { return &Handler{} }

type Config struct{ URL string }
type Svc struct{}

func NewSvc(c *Config) *Svc { return &Svc{} }

// The preset ships a default, the product overrides it: the default backs
// off, so there is exactly one candidate — no DI0004, and no DI0001 either.
func defaultBacksOffForRealProvider() {
	stack.Run(
		di.Default(di.Bind[Store](NewMem)),
		di.Bind[Store](NewPg),
		di.Provide(NewHandler),
	)
}

// Nothing overrides it: a lone default resolves normally.
func loneDefaultResolves() {
	stack.Run(
		di.Default(di.Bind[Store](NewMem)),
		di.Provide(NewHandler),
	)
}

// Back-off is per registration kind, not per API: Supply inside Default is
// auto-configuration too.
func defaultSupplyBacksOff() {
	stack.Run(
		di.Default(di.Supply(&Config{URL: "batteries"})),
		di.Supply(&Config{URL: "product"}),
		di.Provide(NewSvc),
	)
}

// Default composes through Module/Options/Global and keeps the flag.
func defaultThroughCombinators() {
	stack.Run(
		di.Default(di.Options(di.Module("preset", di.Bind[Store](NewMem)))),
		di.Bind[Store](NewPg),
		di.Provide(NewHandler),
	)
}

// Two defaults are still an ambiguity: Default never introduces last-wins
// semantics, it only steps aside for a real registration.
func twoDefaultsStillAmbiguous() {
	stack.Run(
		di.Default(di.Bind[Store](NewMem)),
		di.Default(di.Bind[Store](NewPg)),
		di.Provide(NewHandler), // want `error\[DI0004\]: 2 providers for defaults.Store consumed bare by NewHandler`
	)
}

// Two real providers: unchanged, DI0004 as before.
func twoRealProvidersAmbiguous() {
	stack.Run(
		di.Bind[Store](NewMem),
		di.Bind[Store](NewPg),
		di.Provide(NewHandler), // want `error\[DI0004\]: 2 providers for defaults.Store consumed bare by NewHandler`
	)
}

type LogUser struct{}

func NewLogUser(lg *slog.Logger) *LogUser { return &LogUser{} }

// The framework logger is NOT a default. stack.Base registers *slog.Logger
// with a plain di.Provide(newLogger) — the swappable seam is the slog.Handler
// underneath it — so supplying a second *slog.Logger is a genuine two-provider
// ambiguity that stack.Validate reports at boot. The analyzer says the same
// thing, earlier; the fix is to register a slog.Handler, not a *slog.Logger.
func supplyingASecondLoggerIsAmbiguous() {
	stack.Run(
		di.Supply(slog.Default()),
		di.Provide(NewLogUser), // want `error\[DI0004\]: 2 providers for \*slog.Logger consumed bare by NewLogUser`
	)
}

// With nothing supplied, the kernel-given logger resolves on its own.
func kernelLoggerResolves() {
	stack.Run(
		di.Provide(NewLogUser),
	)
}
