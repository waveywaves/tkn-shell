package parser

import (
	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

// Command represents a single command in the pipeline.
type Command struct {
	Kind   string   `@Ident`
	Action string   `(@Ident)?`
	Args   []string `(@QuotedString | @Flag | @Ident | @RawString)*`
}

// PipelineLine represents a line of piped commands.
type PipelineLine struct {
	Cmds []*Command `@@ ("|" @@)*`
}

var (
	lex = lexer.MustSimple([]lexer.SimpleRule{
		{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_-]*`},
		{Name: "Flag", Pattern: `--[a-zA-Z0-9_-]+`},
		{Name: "QuotedString", Pattern: `"[^"]*"`},
		{Name: "RawString", Pattern: "`[^`]*`"},
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
