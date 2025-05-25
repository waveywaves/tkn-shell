package parser

import (
	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

// Command represents a single command in the pipeline.
type Command struct {
	Kind   string `@Ident`
	Action string `(@Ident)?`
	// Args can be identifiers, quoted strings, flags, or assignments.
	// A RawString at the end of the command is captured by the Script field.
	Args   []string `(@(Assignment (Value | QuotedString | Ident)) | @QuotedString | @Flag | @Ident)*`
	Script string   `(@RawString)?` // Optional script content, typically for steps
}

// PipelineLine represents a line of piped commands.
type PipelineLine struct {
	Cmds []*Command `@@ ("|" @@)*`
}

var (
	lex = lexer.MustSimple([]lexer.SimpleRule{
		// Order is critical: More specific tokens first.
		{Name: "Assignment", Pattern: `[a-zA-Z_][a-zA-Z0-9_-]*=`}, // Matches 'name='
		{Name: "Flag", Pattern: `--[a-zA-Z0-9_-]+`},
		{Name: "QuotedString", Pattern: `"[^\"]*\"`},
		{Name: "RawString", Pattern: "`[^`]*`"},
		{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_-]*`}, // Must come before generic Value
		// Value should be fairly broad for RHS of assignments, but not capture whitespace or pipe.
		{Name: "Value", Pattern: `[^\s\|=]+`},
		{Name: "Punct", Pattern: `\|`},
		{Name: "Whitespace", Pattern: `\s+`},
	})
	parser = participle.MustBuild[PipelineLine](
		participle.Lexer(lex),
		participle.Unquote("QuotedString"),
		participle.Elide("Whitespace"),
	)
)

// ParseLine parses a single line of input into a PipelineLine AST.
func ParseLine(input string) (*PipelineLine, error) {
	return parser.ParseString("", input)
}
