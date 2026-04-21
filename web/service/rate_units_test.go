package service

import "testing"

func TestManagementRateLimitToBps(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{name: "zero", input: 0, want: 0},
		{name: "negative", input: -1, want: 0},
		{name: "kilobytes_per_second", input: 8, want: 8 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ManagementRateLimitToBps(tt.input); got != tt.want {
				t.Fatalf("ManagementRateLimitToBps(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestManagementRateLimitFromBps(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{name: "zero", input: 0, want: 0},
		{name: "negative", input: -1, want: 0},
		{name: "exact", input: 8 * 1024, want: 8},
		{name: "round_up", input: 8*1024 + 1, want: 9},
		{name: "sub_kilobyte", input: 1, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ManagementRateLimitFromBps(tt.input); got != tt.want {
				t.Fatalf("ManagementRateLimitFromBps(%d) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
