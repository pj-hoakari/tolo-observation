package db

import (
	"context"
	"testing"

	"github.com/XSAM/otelsql"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

func TestNormalizeQueryText(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "raw string literal keeps no newlines or tabs",
			query: "\n\t\tINSERT INTO greetings (tenant_public_id, name)\n\t\tVALUES ($1, $2)",
			want:  "INSERT INTO greetings (tenant_public_id, name) VALUES ($1, $2)",
		},
		{
			name:  "leading and trailing whitespace is trimmed",
			query: "  \t SELECT 1 \n ",
			want:  "SELECT 1",
		},
		{
			name:  "runs of spaces collapse into one",
			query: "SELECT     COUNT(*)   FROM   greetings",
			want:  "SELECT COUNT(*) FROM greetings",
		},
		{
			name:  "empty query stays empty",
			query: "",
			want:  "",
		},
		{
			name:  "whitespace only query becomes empty",
			query: " \n\t  \r\n ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeQueryText(tt.query); got != tt.want {
				t.Fatalf("normalizeQueryText(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestQueryTextAttributes(t *testing.T) {
	tests := []struct {
		name string
		// method mirrors what otelsql passes for the span being created.
		method otelsql.Method
		query  string
		// wantText is the expected db.query.text value; an empty string means
		// no attribute must be recorded at all.
		wantText string
	}{
		{
			name:     "method without a query records nothing",
			method:   otelsql.MethodConnectorConnect,
			query:    "",
			wantText: "",
		},
		{
			name:     "whitespace only query records nothing",
			method:   otelsql.MethodConnExec,
			query:    " \n\t ",
			wantText: "",
		},
		{
			name:     "query is recorded normalized",
			method:   otelsql.MethodConnExec,
			query:    "\n\t\tINSERT INTO greetings (tenant_public_id, name)\n\t\tVALUES ($1, $2)",
			wantText: "INSERT INTO greetings (tenant_public_id, name) VALUES ($1, $2)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := queryTextAttributes(context.Background(), tt.method, tt.query, nil)

			if tt.wantText == "" {
				if len(got) != 0 {
					t.Fatalf("queryTextAttributes(%q) = %v, want no attributes", tt.query, got)
				}

				return
			}

			if len(got) != 1 {
				t.Fatalf("queryTextAttributes(%q) = %v, want exactly one attribute", tt.query, got)
			}

			if got[0].Key != semconv.DBQueryTextKey {
				t.Fatalf("queryTextAttributes(%q) key = %v, want %v", tt.query, got[0].Key, semconv.DBQueryTextKey)
			}

			if value := got[0].Value.AsString(); value != tt.wantText {
				t.Fatalf("queryTextAttributes(%q) value = %q, want %q", tt.query, value, tt.wantText)
			}
		})
	}
}
