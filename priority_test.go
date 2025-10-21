package main

import (
	"testing"
)

func TestPRLabelPriority(t *testing.T) {
	tests := []struct {
		label    string
		priority int
	}{
		{"Authored", 1},
		{"Assigned", 2},
		{"Reviewed", 3},
		{"Review Requested", 4},
		{"Commented", 5},
		{"Mentioned", 6},
		{"Unknown", 999},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := getPRLabelPriority(tt.label)
			if got != tt.priority {
				t.Errorf("getPRLabelPriority(%q) = %d, want %d", tt.label, got, tt.priority)
			}
		})
	}
}

func TestIssueLabelPriority(t *testing.T) {
	tests := []struct {
		label    string
		priority int
	}{
		{"Authored", 1},
		{"Assigned", 2},
		{"Commented", 3},
		{"Mentioned", 4},
		{"Unknown", 999},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := getIssueLabelPriority(tt.label)
			if got != tt.priority {
				t.Errorf("getIssueLabelPriority(%q) = %d, want %d", tt.label, got, tt.priority)
			}
		})
	}
}

func TestShouldUpdateLabel_PR(t *testing.T) {
	tests := []struct {
		name         string
		currentLabel string
		newLabel     string
		want         bool
	}{
		{"empty current should update", "", "Mentioned", true},
		{"higher priority should update", "Mentioned", "Authored", true},
		{"same priority should not update", "Authored", "Authored", false},
		{"lower priority should not update", "Authored", "Mentioned", false},
		{"from Mentioned to Reviewed", "Mentioned", "Reviewed", true},
		{"from Authored to Reviewed", "Authored", "Reviewed", false},
		{"from Commented to Assigned", "Commented", "Assigned", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUpdateLabel(tt.currentLabel, tt.newLabel, true)
			if got != tt.want {
				t.Errorf("shouldUpdateLabel(%q, %q, true) = %v, want %v",
					tt.currentLabel, tt.newLabel, got, tt.want)
			}
		})
	}
}

func TestShouldUpdateLabel_Issue(t *testing.T) {
	tests := []struct {
		name         string
		currentLabel string
		newLabel     string
		want         bool
	}{
		{"empty current should update", "", "Mentioned", true},
		{"higher priority should update", "Mentioned", "Authored", true},
		{"same priority should not update", "Authored", "Authored", false},
		{"lower priority should not update", "Authored", "Mentioned", false},
		{"from Mentioned to Commented", "Mentioned", "Commented", true},
		{"from Authored to Commented", "Authored", "Commented", false},
		{"from Commented to Assigned", "Commented", "Assigned", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldUpdateLabel(tt.currentLabel, tt.newLabel, false)
			if got != tt.want {
				t.Errorf("shouldUpdateLabel(%q, %q, false) = %v, want %v",
					tt.currentLabel, tt.newLabel, got, tt.want)
			}
		})
	}
}
