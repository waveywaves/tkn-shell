package repl

import (
	"fmt"
	"os"
	"strings"

	"tkn-shell/internal/engine"
	"tkn-shell/internal/parser"
	"tkn-shell/internal/state"

	"github.com/c-bata/go-prompt"
)

var (
	livePrefix = "tekton> "
	sess       *state.Session
)

func executor(in string) {
	in = strings.TrimSpace(in)
	if in == "" {
		return
	}
	if strings.ToLower(in) == "exit" {
		fmt.Println("Bye!")
		os.Exit(0)
		return
	}

	pipelineLine, err := parser.ParseLine(in)
	if err != nil {
		fmt.Printf("Error parsing command: %v\n", err)
		return
	}

	var prevResult interface{}
	var activeWhenClause *parser.WhenClause // Store the WhenClause if encountered

	for _, cmdWrapper := range pipelineLine.Cmds {
		if cmdWrapper.When != nil {
			activeWhenClause = cmdWrapper.When
			// A WhenClause by itself doesn't produce output or change prevResult directly
			// It modifies the *next* BaseCommand.
			fmt.Printf("Line %d, Col %d: When clause parsed: %d conditions. Will apply to next task.\n", cmdWrapper.Pos.Line, cmdWrapper.Pos.Column, len(activeWhenClause.Conditions))
			continue // Continue to the next command in the pipe, which should be the BaseCommand
		}

		if cmdWrapper.Cmd != nil {
			result, execErr := engine.ExecuteCommand(cmdWrapper.Pos, cmdWrapper.Cmd, sess, prevResult, activeWhenClause)
			if execErr != nil {
				// Error message from engine.ExecuteCommand will already have position info if it came from there.
				// If the error is from a higher level in REPL (e.g. parsing itself), we add it.
				fmt.Printf("Error: %v\n", execErr)
			}
			prevResult = result
			activeWhenClause = nil // Reset WhenClause after it has been applied (or attempted)
		} else {
			// This case should ideally not be reached if parser ensures Command is either When or Cmd
			fmt.Printf("Warning: Encountered a command wrapper that is neither a WhenClause nor a BaseCommand.\n")
		}
	}

	// Update prefix after command execution
	if sess.CurrentPipeline != nil {
		livePrefix = fmt.Sprintf("tekton(pipeline %s)> ", sess.CurrentPipeline.Name)
	} else {
		livePrefix = "tekton> "
	}
}

func completer(d prompt.Document) []prompt.Suggest {
	s := []prompt.Suggest{
		{Text: "when", Description: "Apply a conditional to the next task"},
		{Text: "pipeline", Description: "Manage pipelines"},
		{Text: "task", Description: "Manage tasks"},
		{Text: "step", Description: "Manage steps"},
		{Text: "export", Description: "Export resources"},
		{Text: "apply", Description: "Apply resources to Kubernetes cluster"},
		{Text: "exit", Description: "Exit the shell"},

		// Actions (could be context-dependent)
		{Text: "create", Description: "Create a new resource"},
		{Text: "add", Description: "Add to an existing resource"},
		{Text: "all", Description: "Target all applicable items (e.g., for export)"},
	}

	// TODO: Add suggestions for pipeline names, task names etc. from session
	// TODO: Add suggestions for flags like --image based on context

	return prompt.FilterHasPrefix(s, d.GetWordBeforeCursor(), true)
}

// Run starts the interactive REPL.
func Run() {
	sess = state.NewSession() // Initialize a new session for the REPL

	p := prompt.New(
		executor,
		completer,
		prompt.OptionTitle("tkn-shell"),
		prompt.OptionPrefix(livePrefix), // Initial prefix
		prompt.OptionLivePrefix(func() (string, bool) { // Dynamic prefix
			return livePrefix, true
		}),
		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionSuggestionTextColor(prompt.White),
		prompt.OptionDescriptionBGColor(prompt.DarkGray),
		prompt.OptionDescriptionTextColor(prompt.White),
		prompt.OptionSelectedSuggestionBGColor(prompt.LightGray),
		prompt.OptionSelectedSuggestionTextColor(prompt.Black),
		prompt.OptionSelectedDescriptionBGColor(prompt.LightGray),
		prompt.OptionSelectedDescriptionTextColor(prompt.Black),
		prompt.OptionMaxSuggestion(10),
	)
	fmt.Println("Welcome to tkn-shell. Type 'exit' to quit.")
	p.Run()
}
