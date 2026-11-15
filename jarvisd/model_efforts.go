package main

import "strings"

// supportedModelEfforts — only levels exposed by the panel. OpenAI's GPT-6.1
// Sol and GPT-6 Astra reject "none" (HTTP 400). Older Sol and Luna accept it.
// Keep this keyed by exact model ID: "Sol" alone is not a capability signal.
func supportedModelEfforts(model string) []string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-6.1-sol", "gpt-6-astra":
		return []string{"low", "medium", "high"}
	default:
		return Efforts
	}
}

// normalizedModelEffort protects persisted profiles created before this change,
// imported profiles and background requests, not only the browser editor.
func normalizedModelEffort(model, effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	for _, allowed := range supportedModelEfforts(model) {
		if effort == allowed {
			return effort
		}
	}
	return "low"
}

func modelEffortOptions() map[string][]string {
	options := make(map[string][]string, len(BackendModels))
	for _, model := range BackendModels {
		options[model] = supportedModelEfforts(model)
	}
	return options
}
