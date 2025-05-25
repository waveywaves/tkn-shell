package parser

import (
	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

// Condition represents a single when condition (e.g., left == right)
// For now, simple string comparison is assumed.
// Tekton WhenExpressions are more complex (Input, Operator, Values array).
// This will be mapped to a single WhenExpression in the engine.
type Condition struct {
	Pos      lexer.Position // Populated by Participle
	Left     string         `@(Ident | QuotedString | Value)`
	Operator string         `@("==" | "!=")`
	Right    string         `@(Ident | QuotedString | Value)`
}

// WhenClause represents the 'when' keyword followed by one or more conditions.
// Participle will parse this. In the engine, these will be mapped to Tekton's WhenExpressions.
// For now, we only support one condition for simplicity as per user request.
type WhenClause struct {
	Pos        lexer.Position // Populated by Participle
	Conditions []*Condition   `"when" @@` // Simplified to one condition for now as per user request
}

// BaseCommand holds the fields for regular commands (task, step, pipeline, param, export).
type BaseCommand struct {
	Pos    lexer.Position // Populated by Participle
	Kind   string         `@Ident`
	Action string         `(@Ident)?`
	// Args can be identifiers, quoted strings, flags, assignments, or general values.
	// A RawString at the end of the command is captured by the Script field.
	Args   []string `(@(Assignment (Value | QuotedString | Ident)) | @Value | @QuotedString | @Flag | @Ident)*`
	Script string   `(@RawString)?` // Optional script content, typically for steps
}

// Command is a wrapper that can be a BaseCommand or a WhenClause.
// This allows a WhenClause to appear in the command sequence.
// A WhenClause will affect the *next* BaseCommand in the PipelineLine.
// This is a common way to handle such prefixing/modifying keywords in Participle.
// If a WhenClause is present, the subsequent BaseCommand is conceptually its child.
// This relationship will be handled by the engine.
type Command struct {
	Pos  lexer.Position // Populated by Participle
	When *WhenClause    `( @@`
	Cmd  *BaseCommand   `| @@)` // A command is either a WhenClause or a BaseCommand
}

// PipelineLine represents a line of piped commands.
type PipelineLine struct {
	Pos  lexer.Position // Populated by Participle
	Cmds []*Command     `@@ ("|" @@)*`
}

var (
	lex = lexer.MustSimple([]lexer.SimpleRule{
		// Order is critical: More specific tokens first.
		{Name: "Keywords", Pattern: `when`},
		{Name: "Operators", Pattern: `==|!=`},
		{Name: "Assignment", Pattern: `[a-zA-Z_][a-zA-Z0-9_-]*=`}, // e.g. name=
		{Name: "Flag", Pattern: `--[a-zA-Z0-9_-]+`},
		{Name: "QuotedString", Pattern: `"[^\"]*"`},
		{Name: "RawString", Pattern: "`[^`]*`"},
		{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_-]*`},
		// Value should be less specific than Ident, Flag, Assignment, etc.
		// It captures things like image names with repo/path, or unquoted param values.
		{Name: "Value", Pattern: `[^\s\|=]+`},
		{Name: "Punct", Pattern: `\|`},
		{Name: "Whitespace", Pattern: `\s+`},
	})
	parser = participle.MustBuild[PipelineLine](
		participle.Lexer(lex),
		participle.Unquote("QuotedString"),
		participle.Elide("Whitespace", "Keywords"), // Elide Keywords as they are part of struct tags
		// participle.UseLookahead(2), // May not be needed now
	)
)

// ParseLine parses a single line of input into a PipelineLine AST.
func ParseLine(line string) (*PipelineLine, error) {
	return parser.ParseString("", line)
}
