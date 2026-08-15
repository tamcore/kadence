package chat

// Test-only accessors for the forced-answer notices, so the external test
// package can assert that each limit explains itself distinctly.

// ForcedByIterationCapNotice returns the notice shown when a turn spends its
// whole tool-iteration budget.
func ForcedByIterationCapNotice() string { return forcedByIterationCap.notice() }

// ForcedByContextBudgetNotice returns the notice shown when a turn's gathered
// context no longer fits the budget.
func ForcedByContextBudgetNotice() string { return forcedByContextBudget.notice() }
