package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHeightTracker_IsArchival(t *testing.T) {
	tests := []struct {
		name          string
		pruningWindow int64
		headHeight    int64
		queryHeight   int64
		expected      bool
	}{
		{"within window", 1000, 5000, 4500, false},
		{"at window edge", 1000, 5000, 4000, false},
		{"outside window", 1000, 5000, 3999, true},
		{"zero height (latest)", 1000, 5000, 0, false},
		{"negative height", 1000, 5000, -1, false},
		{"head not set", 1000, 0, 100, false},
		{"no pruning window", 0, 5000, 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ht := NewHeightTracker(tt.pruningWindow)
			if tt.headHeight > 0 {
				ht.SetHead(tt.headHeight)
			}
			assert.Equal(t, tt.expected, ht.IsArchival(tt.queryHeight))
		})
	}
}

func TestHeightTracker_Enabled(t *testing.T) {
	assert.True(t, NewHeightTracker(1000).Enabled())
	assert.False(t, NewHeightTracker(0).Enabled())
	assert.False(t, NewHeightTracker(-1).Enabled())
}

func TestExtractHeight(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		body     string
		expected int64
	}{
		{
			"block with string height",
			"block",
			`{"jsonrpc":"2.0","id":1,"method":"block","params":["500000"]}`,
			500000,
		},
		{
			"block with numeric height",
			"block",
			`{"jsonrpc":"2.0","id":1,"method":"block","params":[500000]}`,
			500000,
		},
		{
			"header.GetByHeight",
			"header.GetByHeight",
			`{"jsonrpc":"2.0","id":1,"method":"header.GetByHeight","params":[100]}`,
			100,
		},
		{
			"blob.Get",
			"blob.Get",
			`{"jsonrpc":"2.0","id":1,"method":"blob.Get","params":[2683915,"AAAA","AA=="]}`,
			2683915,
		},
		{
			"status (no height)",
			"status",
			`{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`,
			0,
		},
		{
			"non-height method",
			"node.Info",
			`{"jsonrpc":"2.0","id":1,"method":"node.Info","params":[]}`,
			0,
		},
		{
			"empty params",
			"block",
			`{"jsonrpc":"2.0","id":1,"method":"block","params":[]}`,
			0,
		},
		{
			"object params with height",
			"block",
			`{"jsonrpc":"2.0","id":1,"method":"block","params":{"height":"12345"}}`,
			12345,
		},
		{
			"block_results",
			"block_results",
			`{"jsonrpc":"2.0","id":1,"method":"block_results","params":["999"]}`,
			999,
		},
		{
			"validators",
			"validators",
			`{"jsonrpc":"2.0","id":1,"method":"validators","params":["50000"]}`,
			50000,
		},
		{
			"share.GetEDS",
			"share.GetEDS",
			`{"jsonrpc":"2.0","id":1,"method":"share.GetEDS","params":[42]}`,
			42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractHeight(tt.method, []byte(tt.body))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseHeight(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int64
	}{
		{"numeric", "12345", 12345},
		{"string", `"12345"`, 12345},
		{"zero", "0", 0},
		{"empty string", `""`, 0},
		{"non-numeric", `"abc"`, 0},
		{"null", "null", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseHeight([]byte(tt.input)))
		})
	}
}
