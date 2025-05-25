package parser

import (
	"reflect"
	"testing"
)

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
					{Kind: "task", Action: "create", Args: []string{"build"}},
					{Kind: "step", Action: "add", Args: []string{"compile", "--image", "alpine"}},
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
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseLine() = %v, want %v", got, tt.want)
			}
		})
	}
}
