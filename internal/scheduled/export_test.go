package scheduled

import (
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// boundedHandoffDefinition builds a handoff definition at the production
// context limit. Test-only: production calls boundedHandoffDefinitionLimit
// with the limit it wants.
func boundedHandoffDefinition(now time.Time, timezone, instruction string, recent []model.Message, visible []provider.ToolDefinition) string {
	return boundedHandoffDefinitionLimit(now, timezone, instruction, recent, visible, maxHandoffContextBytes)
}
