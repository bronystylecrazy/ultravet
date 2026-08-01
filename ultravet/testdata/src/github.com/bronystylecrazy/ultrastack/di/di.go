// Stub of the di kernel: signatures only, for analysis tests.
package di

type Reg struct{ x any }

type Registration = Reg
type App struct{}
type Runner interface{ Go(any) }
type Key string
type Many[T any] []T
type Optional[T any] struct{ v *T }
type Lazy[T any] struct{ f func() T }
type Keyed[T any] struct{ m map[Key]T }
type Family[S any] struct{ m map[Key]S }

func New(regs ...Reg) (*App, error)       { return nil, nil }
func Validate(regs ...Reg) error          { return nil }
func Provide(ctors ...any) Reg            { return Reg{} }
func Default(regs ...Reg) Reg             { return Reg{} }
func Supply(values ...any) Reg            { return Reg{} }
func Bind[I any](ctor any) Reg            { return Reg{} }
func Module(name string, regs ...Reg) Reg { return Reg{} }
func Pkg(regs ...Reg) Reg                 { return Reg{} }
func Options(regs ...Reg) Reg             { return Reg{} }
func Group(regs ...Reg) Reg               { return Reg{} }
func Global(regs ...Reg) Reg              { return Reg{} }
func Tolerate(regs ...Reg) Reg            { return Reg{} }
func Export[T any]() Reg                  { return Reg{} }
func Decorate(fns ...any) Reg             { return Reg{} }
func OnDemand() Reg                       { return Reg{} }

func Scoped(ctors ...any) Reg           { return Reg{} }
func PerKey(keys any, ctors ...any) Reg { return Reg{} }
func Members(ctors ...any) Reg          { return Reg{} }

type Scope[S any] struct{ s *S }

// ProvideOption values may be interleaved with constructors in a di.Provide
// call — di.As[I]() and di.NonCritical.
type ProvideOption interface{ provideOption() }

func As[I any]() ProvideOption { return nil }

var NonCritical ProvideOption
