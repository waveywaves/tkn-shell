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
	for _, cmd := range pipelineLine.Cmds {
		// engine.ExecuteCommand already prints output, so we just capture errors here.
		// The 'prevResult' is passed along, though current engine commands don't heavily use it for piping data.
		result, execErr := engine.ExecuteCommand(cmd, sess, prevResult)
		if execErr != nil {
			fmt.Printf("Error executing command: %v\n", execErr)
			// Optionally, decide if the chain should break on error
			// return
		}
		prevResult = result // Store result for potential use by a subsequent piped command
	}

	// Update prefix after command execution, in case CurrentPipeline changed
	if sess.CurrentPipeline != nil {
		livePrefix = fmt.Sprintf("tekton(pipeline %s)> ", sess.CurrentPipeline.Name)
	} else {
		livePrefix = "tekton> "
	}
}

func completer(d prompt.Document) []prompt.Suggest {
	s := []prompt.Suggest{
		// Keywords
		{Text: "pipeline", Description: "Manage pipelines"},
		{Text: "task", Description: "Manage tasks"},
		{Text: "step", Description: "Manage steps"},
		{Text: "export", Description: "Export resources"},
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
