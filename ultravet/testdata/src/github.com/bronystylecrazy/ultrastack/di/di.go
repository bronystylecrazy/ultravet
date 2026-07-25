// Stub of the di kernel: signatures only, for analysis tests.
package di

type Registration struct{ x any }
type App struct{}
type Runner interface{ Go(any) }
type Key string
type Many[T any] []T
type Optional[T any] struct{ v *T }
type Lazy[T any] struct{ f func() T }
type Keyed[T any] struct{ m map[Key]T }
type Family[S any] struct{ m map[Key]S }

func New(regs ...Registration) (*App, error)                { return nil, nil }
func Validate(regs ...Registration) error                   { return nil }
func Provide(ctors ...any) Registration                     { return Registration{} }
func Default(regs ...Registration) Registration             { return Registration{} }
func Supply(values ...any) Registration                     { return Registration{} }
func Bind[I any](ctor any) Registration                     { return Registration{} }
func Module(name string, regs ...Registration) Registration { return Registration{} }
func Options(regs ...Registration) Registration             { return Registration{} }
func Global(regs ...Registration) Registration              { return Registration{} }
func Tolerate(regs ...Registration) Registration            { return Registration{} }
func Export[T any]() Registration                           { return Registration{} }
func Decorate(fns ...any) Registration                      { return Registration{} }
func OnDemand() Registration                                { return Registration{} }

func Scoped(ctors ...any) Registration           { return Registration{} }
func PerKey(keys any, ctors ...any) Registration { return Registration{} }
func Members(ctors ...any) Registration          { return Registration{} }

type Scope[S any] struct{ s *S }

// ProvideOption values may be interleaved with constructors in a di.Provide
// call — di.As[I]() and di.NonCritical.
type ProvideOption interface{ provideOption() }

func As[I any]() ProvideOption { return nil }

var NonCritical ProvideOption
