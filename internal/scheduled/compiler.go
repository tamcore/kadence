package scheduled

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tamcore/kadence/internal/provider"
)

const interactiveCredentialsTool = "kadence__request_credentials" // #nosec G101 -- a tool name, not a credential

// QuestionKind controls how a Scheduled clarification is rendered.
type QuestionKind string

const (
	QuestionKindSingleSelect QuestionKind = "single_select"
	QuestionKindMultiSelect  QuestionKind = "multi_select"
	QuestionKindText         QuestionKind = "text"
)

// QuestionOption is one labelled value available in a selection question.
type QuestionOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// QuestionCard asks one focused clarification during task refinement.
type QuestionCard struct {
	ID          string           `json:"id"`
	Prompt      string           `json:"prompt"`
	Kind        QuestionKind     `json:"kind"`
	Options     []QuestionOption `json:"options,omitempty"`
	AllowCustom bool             `json:"allowCustom"`
	Optional    bool             `json:"optional"`
}

// TaskKind selects the unattended task's semantic behavior.
type TaskKind string

const (
	TaskKindReminder   TaskKind = "reminder"
	TaskKindData       TaskKind = "data"
	TaskKindMonitoring TaskKind = "monitoring"
)

// ExecutionMode determines whether an occurrence needs provider inference.
type ExecutionMode string

const (
	ExecutionModeStatic ExecutionMode = "static"
	ExecutionModeData   ExecutionMode = "data"
)

// DeliveryPolicy controls when a task produces visible activity.
type DeliveryPolicy string

const (
	DeliveryPolicyAlways   DeliveryPolicy = "always"
	DeliveryPolicyOnChange DeliveryPolicy = "on_change"
)

// InitialRun controls activation behavior before the first scheduled occurrence.
type InitialRun string

const (
	InitialRunWait     InitialRun = "wait"
	InitialRunPreview  InitialRun = "preview"
	InitialRunBaseline InitialRun = "baseline"
)

// Proposal is a complete, confirmed-before-persistence task definition.
type Proposal struct {
	Version         int            `json:"version"`
	Name            string         `json:"name"`
	TaskKind        TaskKind       `json:"taskKind"`
	CompiledPrompt  string         `json:"compiledPrompt"`
	ExecutionMode   ExecutionMode  `json:"executionMode"`
	Schedule        Schedule       `json:"schedule"`
	Timezone        string         `json:"timezone"`
	AuthorizedTools []string       `json:"authorizedTools"`
	DeliveryPolicy  DeliveryPolicy `json:"deliveryPolicy"`
	InitialRun      InitialRun     `json:"initialRun"`
	StopCondition   string         `json:"stopCondition,omitempty"`
	StaticMessage   string         `json:"staticMessage,omitempty"`
}

// Refinement is one provider result: optional assistant text and exactly one
// structured question or final proposal.
type Refinement struct {
	Text     string        `json:"assistantText,omitempty"`
	Question *QuestionCard `json:"question,omitempty"`
	Proposal *Proposal     `json:"proposal,omitempty"`
}

// CompilerConfig controls one tool-free definition-model request.
type CompilerConfig struct {
	Model     string
	MaxTokens int
}

// Compiler refines a complete Scheduled conversation through the primary
// provider. It never passes provider tools to this definition request.
type Compiler struct {
	provider provider.Provider
	cfg      CompilerConfig
}

// NewCompiler constructs a provider-independent task compiler.
func NewCompiler(p provider.Provider, cfg CompilerConfig) *Compiler {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 2048
	}
	return &Compiler{provider: p, cfg: cfg}
}

// Refine sends the complete Scheduled conversation to the primary provider and
// validates its single structured refinement response.
func (c *Compiler) Refine(ctx context.Context, history []provider.Message, available []provider.ToolDefinition, nextVersion int) (Refinement, error) {
	if c == nil || c.provider == nil {
		return Refinement{}, errors.New("scheduled: compiler provider is required")
	}
	if nextVersion <= 0 {
		return Refinement{}, errors.New("scheduled: proposal version must be positive")
	}

	tools := availableToolMap(available)
	req := provider.ChatRequest{
		Messages:    make([]provider.Message, 0, len(history)+1),
		Model:       c.cfg.Model,
		MaxTokens:   c.cfg.MaxTokens,
		Temperature: 0,
	}
	req.Messages = append(req.Messages, provider.Message{Role: "system", Content: compilerSystemPrompt(tools)})
	req.Messages = append(req.Messages, history...)

	response, err := c.provider.StreamChat(ctx, req, func(string) error { return nil })
	if err != nil {
		return Refinement{}, fmt.Errorf("scheduled: refine task: %w", err)
	}

	var decoded Refinement
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		return Refinement{}, fmt.Errorf("scheduled: decode refinement: %w", err)
	}
	if (decoded.Question == nil) == (decoded.Proposal == nil) {
		return Refinement{}, errors.New("scheduled: refinement must contain exactly one question or proposal")
	}
	decoded.Text = normalizeWhitespace(decoded.Text)
	if decoded.Question != nil {
		if err := validateQuestion(decoded.Question); err != nil {
			return Refinement{}, err
		}
		return decoded, nil
	}
	if err := validateProposal(decoded.Proposal, tools, nextVersion, time.Now()); err != nil {
		return Refinement{}, err
	}
	return decoded, nil
}

func compilerSystemPrompt(tools map[string]provider.ToolDefinition) string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("You refine one Scheduled task from the complete conversation. Reply only with one JSON object. ")
	b.WriteString("It may include assistantText and must include exactly one of question or proposal. ")
	b.WriteString("Ask one focused question at a time. A question has id, prompt, kind (single_select, multi_select, or text), options [{label,value}], allowCustom, and optional. ")
	b.WriteString("A final proposal has version, name, taskKind (reminder, data, monitoring), compiledPrompt, executionMode (static, data), schedule {at,dtStart,rrule,timezone}, timezone, authorizedTools, deliveryPolicy (always, on_change), initialRun (wait, preview, baseline), optional stopCondition, and optional staticMessage. ")
	b.WriteString("Use IANA timezones; schedule.timezone must equal timezone. Reminders are static, have no authorized tools, always deliver, and require staticMessage. Data and monitoring tasks use data mode and cannot have staticMessage. Monitoring requires a recurring rrule and uses on_change delivery; only monitoring may use stopCondition. ")
	b.WriteString("Use only these currently available tools by exact name:\n")
	for _, name := range names {
		b.WriteString("- ")
		b.WriteString(name)
		if description := normalizeWhitespace(tools[name].Description); description != "" {
			b.WriteString(": ")
			b.WriteString(description)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func availableToolMap(available []provider.ToolDefinition) map[string]provider.ToolDefinition {
	tools := make(map[string]provider.ToolDefinition, len(available))
	for _, tool := range available {
		name := normalizeWhitespace(tool.Name)
		if name == "" || name == interactiveCredentialsTool {
			continue
		}
		tool.Name = name
		tool.Description = normalizeWhitespace(tool.Description)
		tools[name] = tool
	}
	return tools
}

var questionID = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func validateQuestion(question *QuestionCard) error {
	question.ID = normalizeWhitespace(question.ID)
	question.Prompt = normalizeWhitespace(question.Prompt)
	if !questionID.MatchString(question.ID) {
		return errors.New("scheduled: question id must be stable lowercase text")
	}
	if question.Prompt == "" {
		return errors.New("scheduled: question prompt is required")
	}
	switch question.Kind {
	case QuestionKindText:
		if len(question.Options) != 0 {
			return errors.New("scheduled: text question cannot have options")
		}
		return nil
	case QuestionKindSingleSelect, QuestionKindMultiSelect:
		if len(question.Options) == 0 {
			return errors.New("scheduled: selection question requires options")
		}
	default:
		return fmt.Errorf("scheduled: unsupported question kind %q", question.Kind)
	}

	seen := make(map[string]struct{}, len(question.Options))
	for i := range question.Options {
		option := &question.Options[i]
		option.Label = normalizeWhitespace(option.Label)
		option.Value = normalizeWhitespace(option.Value)
		if option.Label == "" || option.Value == "" {
			return errors.New("scheduled: question options require label and value")
		}
		if _, exists := seen[option.Value]; exists {
			return errors.New("scheduled: question option values must be unique")
		}
		seen[option.Value] = struct{}{}
	}
	return nil
}

func validateProposal(proposal *Proposal, available map[string]provider.ToolDefinition, nextVersion int, now time.Time) error {
	proposal.Version = nextVersion
	proposal.Name = normalizeWhitespace(proposal.Name)
	proposal.CompiledPrompt = normalizeWhitespace(proposal.CompiledPrompt)
	proposal.Timezone = normalizeWhitespace(proposal.Timezone)
	proposal.Schedule.Timezone = normalizeWhitespace(proposal.Schedule.Timezone)
	proposal.StopCondition = normalizeWhitespace(proposal.StopCondition)
	proposal.StaticMessage = normalizeWhitespace(proposal.StaticMessage)
	if proposal.Name == "" || proposal.CompiledPrompt == "" {
		return errors.New("scheduled: proposal name and compiled prompt are required")
	}
	if proposal.Timezone == "" || proposal.Schedule.Timezone != proposal.Timezone {
		return errors.New("scheduled: proposal timezone must match schedule timezone")
	}
	if err := proposal.Schedule.Validate(now); err != nil {
		return fmt.Errorf("scheduled: invalid proposal schedule: %w", err)
	}
	if err := validateAuthorizedTools(&proposal.AuthorizedTools, available); err != nil {
		return err
	}
	if err := validateTaskEnums(proposal); err != nil {
		return err
	}
	return validateTaskConsistency(proposal)
}

func validateAuthorizedTools(authorized *[]string, available map[string]provider.ToolDefinition) error {
	seen := make(map[string]struct{}, len(*authorized))
	for i, name := range *authorized {
		name = normalizeWhitespace(name)
		if _, ok := available[name]; !ok {
			return fmt.Errorf("scheduled: authorized tool %q is unavailable", name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("scheduled: duplicate authorized tool %q", name)
		}
		seen[name] = struct{}{}
		(*authorized)[i] = name
	}
	sort.Strings(*authorized)
	return nil
}

func validateTaskEnums(proposal *Proposal) error {
	switch proposal.TaskKind {
	case TaskKindReminder, TaskKindData, TaskKindMonitoring:
	default:
		return fmt.Errorf("scheduled: unsupported task kind %q", proposal.TaskKind)
	}
	switch proposal.ExecutionMode {
	case ExecutionModeStatic, ExecutionModeData:
	default:
		return fmt.Errorf("scheduled: unsupported execution mode %q", proposal.ExecutionMode)
	}
	switch proposal.DeliveryPolicy {
	case DeliveryPolicyAlways, DeliveryPolicyOnChange:
	default:
		return fmt.Errorf("scheduled: unsupported delivery policy %q", proposal.DeliveryPolicy)
	}
	switch proposal.InitialRun {
	case InitialRunWait, InitialRunPreview, InitialRunBaseline:
		return nil
	default:
		return fmt.Errorf("scheduled: unsupported initial run %q", proposal.InitialRun)
	}
}

func validateTaskConsistency(proposal *Proposal) error {
	switch proposal.TaskKind {
	case TaskKindReminder:
		if proposal.ExecutionMode != ExecutionModeStatic || len(proposal.AuthorizedTools) != 0 || proposal.DeliveryPolicy != DeliveryPolicyAlways || proposal.StaticMessage == "" || proposal.StopCondition != "" {
			return errors.New("scheduled: reminder must be a static always-delivered message without tools or stop condition")
		}
		if proposal.InitialRun == InitialRunBaseline {
			return errors.New("scheduled: reminder cannot use a monitoring baseline")
		}
	case TaskKindData:
		if proposal.ExecutionMode != ExecutionModeData || proposal.StaticMessage != "" || proposal.StopCondition != "" {
			return errors.New("scheduled: data task must use data mode without static message or stop condition")
		}
		if proposal.InitialRun == InitialRunBaseline {
			return errors.New("scheduled: data task cannot use a monitoring baseline")
		}
	case TaskKindMonitoring:
		if proposal.ExecutionMode != ExecutionModeData || proposal.StaticMessage != "" || proposal.DeliveryPolicy != DeliveryPolicyOnChange || proposal.Schedule.RRULE == "" {
			return errors.New("scheduled: monitoring must recur in data mode with on-change delivery")
		}
	}
	return nil
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
