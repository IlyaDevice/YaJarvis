package main

import (
	"reflect"
	"testing"
)

func TestModelReasoningCapabilities(t *testing.T) {
	tests := []struct {
		model string
		want  []string
	}{
		{"gpt-6.1-sol", []string{"low", "medium", "high"}},
		{"gpt-6-astra", []string{"low", "medium", "high"}},
		{"gpt-6-sol", []string{"none", "low", "medium", "high"}},
		{"gpt-6-luna", []string{"none", "low", "medium", "high"}},
		{"gpt-6.1-terra", []string{"none", "low", "medium", "high"}},
	}
	for _, test := range tests {
		if got := supportedModelEfforts(test.model); !reflect.DeepEqual(got, test.want) {
			t.Errorf("model %s: got %v; want %v", test.model, got, test.want)
		}
		if got := modelEffortOptions()[test.model]; !reflect.DeepEqual(got, test.want) {
			t.Errorf("API picker for %s: got %v; want %v", test.model, got, test.want)
		}
	}
}

func TestNormalizeModelEffort(t *testing.T) {
	tests := []struct{ model, effort, want string }{
		{"gpt-6.1-sol", "none", "low"},
		{"gpt-6.1-sol", "minimal", "low"},
		{"gpt-6.1-sol", "medium", "medium"},
		{"gpt-6.1-sol", "high", "high"},
		{"gpt-6.1-sol", "", "low"},
		{"gpt-6-astra", "none", "low"},
		{"gpt-6-sol", "none", "none"},
		{"gpt-6-luna", "none", "none"},
		{"gpt-6.1-terra", "none", "none"},
		{"gpt-5.6-luna", "none", "none"},
		{"gpt-6.1-sol", "unexpected-effort", "low"},
		{" GPT-6.1-SOL ", " NONE ", "low"},
	}
	for _, test := range tests {
		if got := normalizedModelEffort(test.model, test.effort); got != test.want {
			t.Errorf("model=%q effort=%q: got %q; want %q", test.model, test.effort, got, test.want)
		}
	}
}
