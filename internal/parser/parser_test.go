package parser

import (
	"reflect"
	"testing"

	"github.com/alecthomas/participle/v2/lexer"
)

// zeroOutPositions recursively sets all Pos fields to lexer.Position{}
func zeroOutPositions(pl *PipelineLine) {
	if pl == nil {
		return
	}
	pl.Pos = lexer.Position{}
	for _, cmd := range pl.Cmds {
		if cmd == nil {
			continue
		}
		cmd.Pos = lexer.Position{}
		if cmd.Cmd != nil {
			cmd.Cmd.Pos = lexer.Position{}
			// BaseCommand Args and Script don't have Pos fields directly in their string/[]string types
		}
		if cmd.When != nil {
			cmd.When.Pos = lexer.Position{}
			for _, cond := range cmd.When.Conditions {
				if cond != nil {
					cond.Pos = lexer.Position{}
				}
			}
		}
	}
}

func TestParseLine(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *PipelineLine
		wantErr bool
	}{
		{
			name:  "simple pipeline",
			input: "task create build | step add compile --image alpine",
			want: &PipelineLine{
				Cmds: []*Command{
					{Cmd: &BaseCommand{Kind: "task", Action: "create", Args: []string{"build"}}},
					{Cmd: &BaseCommand{Kind: "step", Action: "add", Args: []string{"compile", "--image", "alpine"}}},
				},
			},
			wantErr: false,
		},
		{
			name:  "pipeline with when clause",
			input: "pipeline create p | when env == \"prod\" | task create deploy",
			want: &PipelineLine{
				Cmds: []*Command{
					{Cmd: &BaseCommand{Kind: "pipeline", Action: "create", Args: []string{"p"}}},
					{When: &WhenClause{Conditions: []*Condition{{
						Left: "env", Operator: "==", Right: "prod", // Parser unquotes QuotedString
					}}}},
					{Cmd: &BaseCommand{Kind: "task", Action: "create", Args: []string{"deploy"}}},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLine(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLine() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// Zero out positions before comparison if successful parse
			if err == nil && got != nil {
				zeroOutPositions(got)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseLine() = %v, want %v", got, tt.want)
			}
		})
	}
}
