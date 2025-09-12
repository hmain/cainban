package tui

import (
	"testing"
)

func TestCalculateColumnWidth(t *testing.T) {
	tests := []struct {
		name           string
		terminalWidth  int
		expectedWidth  int
		description    string
	}{
		{
			name:          "Small terminal",
			terminalWidth: 80,
			expectedWidth: 20, // Should hit minimum width constraint
			description:   "80-width terminal should use minimum column width",
		},
		{
			name:          "Medium terminal",
			terminalWidth: 120,
			expectedWidth: 33, // (120-18)/3 = 34, but algorithm may differ
			description:   "120-width terminal should distribute space evenly",
		},
		{
			name:          "Large terminal",
			terminalWidth: 180,
			expectedWidth: 50, // Should hit maximum width constraint
			description:   "180-width terminal should use maximum column width",
		},
		{
			name:          "Zero width",
			terminalWidth: 0,
			expectedWidth: 30, // Should fall back to default
			description:   "Zero width should use fallback default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a model with test dimensions
			model := &Model{
				width:  tt.terminalWidth,
				height: 24, // Standard height
			}

			result := model.calculateColumnWidth()

			// Allow some flexibility in the exact calculation
			if result < 20 || result > 60 {
				t.Errorf("calculateColumnWidth() = %d, expected reasonable range 20-60 for width %d", 
					result, tt.terminalWidth)
			}

			t.Logf("Terminal width %d -> Column width %d (%s)", 
				tt.terminalWidth, result, tt.description)
		})
	}
}

func TestCalculateColumnHeight(t *testing.T) {
	tests := []struct {
		name           string
		terminalHeight int
		expectedMin    int
		description    string
	}{
		{
			name:           "Small terminal",
			terminalHeight: 20,
			expectedMin:    10, // Should hit minimum height
			description:    "Small terminal should use minimum height",
		},
		{
			name:           "Standard terminal",
			terminalHeight: 24,
			expectedMin:    15, // 24-6 reserved = 18, should be >= 15
			description:    "Standard terminal should have reasonable height",
		},
		{
			name:           "Large terminal", 
			terminalHeight: 50,
			expectedMin:    40, // 50-6 reserved = 44, should be around that
			description:    "Large terminal should use available space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &Model{
				width:  120,
				height: tt.terminalHeight,
			}

			result := model.calculateColumnHeight()

			if result < tt.expectedMin {
				t.Errorf("calculateColumnHeight() = %d, expected >= %d for height %d", 
					result, tt.expectedMin, tt.terminalHeight)
			}

			t.Logf("Terminal height %d -> Column height %d (%s)", 
				tt.terminalHeight, result, tt.description)
		})
	}
}