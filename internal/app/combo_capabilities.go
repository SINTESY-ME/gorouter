package app

import (
	"context"

	"github.com/jhon/gorouter/internal/domain"
)

// A combo carries no metadata of its own: it is a set of members, each with a
// catalog entry, and what the combo can do is what its members can. The
// aggregate is computed on read — members are re-synced on their own schedule,
// so a stored aggregate would silently go stale.

// ComboCapabilities resolves a combo's aggregated capabilities from the
// catalog, following nested combos. Members that resolve to nothing are
// counted but contribute nothing.
func (s *ComboService) Capabilities(ctx context.Context, c domain.Combo) domain.ComboCapabilities {
	members := 0
	var entries []domain.ModelEntry
	s.collectMembers(ctx, c, 0, map[string]bool{c.Name: true}, &members, &entries)
	return AggregateComboCapabilities(entries, members)
}

// maxCapabilityDepth mirrors the router's own recursion limit for nested
// combos; beyond it a member is left unresolved rather than chased forever.
const maxCapabilityDepth = 8

func (s *ComboService) collectMembers(ctx context.Context, c domain.Combo, depth int, seen map[string]bool, members *int, out *[]domain.ModelEntry) {
	if depth > maxCapabilityDepth || s.Models == nil {
		return
	}
	for _, id := range c.Models {
		if seen[id] {
			continue
		}
		seen[id] = true
		if entry, err := s.Models.Get(ctx, id); err == nil && entry != nil {
			*members++
			*out = append(*out, *entry)
			continue
		}
		// Not a catalog entry: it may be a nested combo. GetByName is the same
		// lookup the router uses to decide that a member is a combo. A nested
		// combo is not counted as a member itself — what it reaches is.
		nested := (*domain.Combo)(nil)
		if s.Repo != nil {
			if found, err := s.Repo.GetByName(ctx, id); err == nil && found != nil {
				nested = found
			}
		}
		if nested == nil {
			*members++ // counted, contributes nothing: an unknown member
			continue
		}
		s.collectMembers(ctx, *nested, depth+1, seen, members, out)
	}
}

// AggregateComboCapabilities folds the catalog entries of a combo's members
// into one statement. Context and MaxOutputTokens take the largest value any
// member reports, because the router skips members whose window cannot fit the
// prompt: the combo serves the prompt as long as one member fits. Flags are
// unioned — vision or tools on a single member is a capability of the combo.
//
// Reasoning follows the same union, with one rule that is deliberately not a
// union: Mandatory is true only when every member that stated anything says
// reasoning cannot be disabled. A level the caller may ask for is a level at
// least one member can honour; disabling reasoning everywhere is a different
// claim, and one member that refuses it is enough to make it false.
func AggregateComboCapabilities(entries []domain.ModelEntry, members int) domain.ComboCapabilities {
	caps := domain.ComboCapabilities{Members: members, MembersKnown: len(entries)}
	defaults := make([]string, 0, len(entries))
	mandatory := len(entries) > 0
	for _, e := range entries {
		if e.Context > caps.Context {
			caps.Context = e.Context
		}
		if e.MaxOutputTokens > caps.MaxOutputTokens {
			caps.MaxOutputTokens = e.MaxOutputTokens
		}
		caps.SupportsVision = caps.SupportsVision || e.SupportsVision
		caps.SupportsToolCall = caps.SupportsToolCall || e.SupportsToolCall

		member := reasoningCapabilitiesFromModelEntry(e)
		mandatory = mandatory && member.Mandatory
		caps.Reasoning = unionReasoningCapabilities(caps.Reasoning, member)
		if member.DefaultEffort != "" {
			defaults = append(defaults, member.DefaultEffort)
		}
	}
	caps.Reasoning.Mandatory = mandatory
	caps.Reasoning.Efforts = domain.SortEfforts(caps.Reasoning.Efforts)
	caps.Reasoning.DefaultEffort = agreedDefaultEffort(defaults)
	if len(caps.Reasoning.Efforts) > 0 {
		caps.Reasoning.EffortsStated = true
	}
	return caps
}

// unionReasoningCapabilities merges one member's reasoning facts into the
// combo's. Unlike the chain merge, ladders union here: a level one member
// honours is a level the combo can be asked for, since every candidate adapts
// the request to its own ladder.
func unionReasoningCapabilities(acc, member domain.ReasoningCapabilities) domain.ReasoningCapabilities {
	return domain.ReasoningCapabilities{
		Known:                          acc.Known || member.Known,
		SupportsReasoning:              acc.SupportsReasoning || member.SupportsReasoning,
		SupportsMinimalReasoningEffort: acc.SupportsMinimalReasoningEffort || member.SupportsMinimalReasoningEffort,
		SupportsLowReasoningEffort:     acc.SupportsLowReasoningEffort || member.SupportsLowReasoningEffort,
		SupportsXHighReasoningEffort:   acc.SupportsXHighReasoningEffort || member.SupportsXHighReasoningEffort,
		SupportsMaxReasoningEffort:     acc.SupportsMaxReasoningEffort || member.SupportsMaxReasoningEffort,
		Efforts:                        append(append([]string{}, acc.Efforts...), member.Efforts...),
		EffortsStated:                  acc.EffortsStated || member.EffortsStated,
	}
}

// agreedDefaultEffort returns the default only when every member that declared
// one declared the same. Different members defaulting to different levels is
// not a fact about the combo, and picking one would be a guess.
func agreedDefaultEffort(defaults []string) string {
	if len(defaults) == 0 {
		return ""
	}
	for _, d := range defaults {
		if d != defaults[0] {
			return ""
		}
	}
	return defaults[0]
}
