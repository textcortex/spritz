package main

import (
	"reflect"
	"testing"
)

func TestParseWatchNamespaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		raw   string
		want  []string
	}{
		{
			name: "empty",
			raw:  "",
			want: nil,
		},
		{
			name: "whitespace only",
			raw:  "   ",
			want: nil,
		},
		{
			name: "single namespace",
			raw:  "spritz-staging",
			want: []string{"spritz-staging"},
		},
		{
			name: "multiple namespaces",
			raw:  "spritz-staging,spritz-system-staging",
			want: []string{"spritz-staging", "spritz-system-staging"},
		},
		{
			name: "trims and deduplicates",
			raw:  " spritz-staging , spritz-system-staging , spritz-staging ,, ",
			want: []string{"spritz-staging", "spritz-system-staging"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseWatchNamespaces(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseWatchNamespaces(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}
