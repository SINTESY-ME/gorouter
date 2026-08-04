package app

import (
	"context"
	"fmt"

	"github.com/jhon/gorouter/internal/domain"
)

// hookFactories maps hook names to constructors. Built-in hooks self-register
// in init(); the composition root reads the enabled list from settings and
// builds the pipeline via NewHookPipeline. Factories return any; Register()
// wires each instance to every hook point it implements.
var hookFactories = map[string]func() any{}

// RegisterHook registers a hook factory under name. Built-in hooks call this
// from init(); the composition root builds the active pipeline.
func RegisterHook(name string, f func() any) {
	hookFactories[name] = f
}

// HookNames returns the registered hook names, for config/docs.
func HookNames() []string {
	names := make([]string, 0, len(hookFactories))
	for n := range hookFactories {
		names = append(names, n)
	}
	return names
}

// NewHookPipeline builds a pipeline from the enabled hook names. Unknown names
// are rejected so a misconfiguration fails fast at startup. An empty list
// returns a nil pipeline — the router's nil-check then short-circuits every
// hook point at zero cost.
func NewHookPipeline(names []string) (*HookPipeline, error) {
	if len(names) == 0 {
		return nil, nil
	}
	p := &HookPipeline{}
	for _, name := range names {
		f, ok := hookFactories[name]
		if !ok {
			return nil, fmt.Errorf("unknown hook %q (available: %v)", name, HookNames())
		}
		p.Register(f())
	}
	return p, nil
}

// HookPipeline runs registered hooks at each point in the request lifecycle.
// It is nil-safe on the router: when no hooks are registered the field is nil
// and every Run* is skipped with zero allocation. Hooks run in registration
// order; the first error short-circuits (fail-closed).
type HookPipeline struct {
	preCall         []domain.PreCallHook
	postCall        []domain.PostCallHook
	postCallFailure []domain.PostCallFailureHook
}

// Register adds h to every hook point it implements. Called once at startup.
func (p *HookPipeline) Register(h any) {
	if v, ok := h.(domain.PreCallHook); ok {
		p.preCall = append(p.preCall, v)
	}
	if v, ok := h.(domain.PostCallHook); ok {
		p.postCall = append(p.postCall, v)
	}
	if v, ok := h.(domain.PostCallFailureHook); ok {
		p.postCallFailure = append(p.postCallFailure, v)
	}
}

// Empty reports whether no hooks are registered at any point.
func (p *HookPipeline) Empty() bool {
	return len(p.preCall) == 0 && len(p.postCall) == 0 && len(p.postCallFailure) == 0
}

// HasPostCall reports whether at least one PostCallHook is registered.
func (p *HookPipeline) HasPostCall() bool { return len(p.postCall) > 0 }

// HasPostCallFailure reports whether at least one PostCallFailureHook is
// registered.
func (p *HookPipeline) HasPostCallFailure() bool { return len(p.postCallFailure) > 0 }

// RunPreCall runs all pre-call hooks in registration order, short-circuiting
// on the first error. Hooks may mutate hc.
func (p *HookPipeline) RunPreCall(ctx context.Context, hc *domain.HookContext) error {
	for _, h := range p.preCall {
		if err := h.PreCall(ctx, hc); err != nil {
			return err
		}
	}
	return nil
}

// RunPostCall runs all post-call hooks. Errors are fail-closed: the caller
// treats the request as failed.
func (p *HookPipeline) RunPostCall(ctx context.Context, hc *domain.HookContext, res *domain.HookResponse) error {
	for _, h := range p.postCall {
		if err := h.PostCall(ctx, hc, res); err != nil {
			return err
		}
	}
	return nil
}

// RunPostCallFailure runs failure hooks; the first non-nil returned error is
// returned to replace the original error sent to the client.
func (p *HookPipeline) RunPostCallFailure(ctx context.Context, hc *domain.HookContext, status int, err error) error {
	for _, h := range p.postCallFailure {
		if nerr := h.PostCallFailure(ctx, hc, status, err); nerr != nil {
			return nerr
		}
	}
	return nil
}
