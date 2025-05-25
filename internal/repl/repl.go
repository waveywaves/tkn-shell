package repl

import (
	"fmt"
	"os"
	"strings"

	"tkn-shell/internal/engine"
	"tkn-shell/internal/feedback"
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
	if strings.ToLower(in) == "exit" || strings.ToLower(in) == "quit" {
		feedback.Infof("Bye!")
		os.Exit(0)
		return
	}

	if strings.ToLower(in) == "help" {
		printHelp()
		return
	}

	pipelineLine, err := parser.ParseLine(in)
	if err != nil {
		feedback.Errorf("Parsing command: %v", err)
		return
	}

	var prevResult interface{}
	var activeWhenClause *parser.WhenClause // Store the WhenClause if encountered

	for _, cmdWrapper := range pipelineLine.Cmds {
		if cmdWrapper.When != nil {
			activeWhenClause = cmdWrapper.When
			feedback.Infof("Line %d, Col %d: When clause parsed: %d conditions. Will apply to next task.", cmdWrapper.Pos.Line, cmdWrapper.Pos.Column, len(activeWhenClause.Conditions))
			continue
		}

		if cmdWrapper.Cmd != nil {
			result, execErr := engine.ExecuteCommand(cmdWrapper.Pos, cmdWrapper.Cmd, sess, prevResult, activeWhenClause)
			if execErr != nil {
				feedback.Errorf("%v", execErr)
			} else if result != nil {
				switch v := result.(type) {
				case []string:
					for _, line := range v {
						feedback.Infof(line)
					}
				case []byte:
					feedback.Infof(string(v))
				}
			}
			prevResult = result
			activeWhenClause = nil
		} else {
			feedback.Errorf("Warning: Encountered a command wrapper that is neither a WhenClause nor a BaseCommand.")
		}
	}

	if sess.CurrentPipeline != nil {
		livePrefix = fmt.Sprintf("tekton(pipeline %s)> ", sess.CurrentPipeline.Name)
	} else {
		livePrefix = "tekton> "
	}
}

func printHelp() {
	feedback.Infof("tkn-shell Help:")
	feedback.Infof("  Core Commands (Keywords):")
	feedback.Infof("    pipeline   - Manage pipelines (create, select)")
	feedback.Infof("    task       - Manage tasks (create, select)")
	feedback.Infof("    step       - Add steps to tasks (add --image <img_name> [script])")
	feedback.Infof("    param      - Set parameters for tasks (name=value)")
	feedback.Infof("    when       - Define conditional execution (e.g., when input == \"val\" | task create ...)")
	feedback.Infof("    list       - List resources (tasks, pipelines, stepactions)")
	feedback.Infof("    show       - Show YAML for a resource (task <name>, pipeline <name>)")
	feedback.Infof("    export     - Export all defined resources to YAML (all)")
	feedback.Infof("    apply      - Apply all defined resources to Kubernetes (all <namespace>)")
	feedback.Infof("    undo       - Revert the last modification (pipeline/task create, step add, param set).")
	feedback.Infof("    reset      - Clear the current session state and undo history.")
	feedback.Infof("")
	feedback.Infof("  Syntax Tips:")
	feedback.Infof("    - Chain commands using '|' (e.g., pipeline create foo | task create bar)")
	feedback.Infof("    - Arguments are space-separated.")
	feedback.Infof("    - Use `backticks` for multi-line scripts in 'step add'. Script content is `echo hello`.")
	feedback.Infof("    - Use \"double quotes\" or 'single quotes' for arguments with spaces (usually for values).")
	feedback.Infof("")
	feedback.Infof("  Exiting:")
	feedback.Infof("    help       - Show this help message.")
	feedback.Infof("    exit       - Quit the shell.")
	feedback.Infof("    quit       - Quit the shell.")
	feedback.Infof("")
}

func completer(d prompt.Document) []prompt.Suggest {
	s := []prompt.Suggest{
		{Text: "help", Description: "Show help information"},
		{Text: "when", Description: "Apply a conditional to the next task"},
		{Text: "pipeline", Description: "Manage pipelines"},
		{Text: "task", Description: "Manage tasks"},
		{Text: "step", Description: "Manage steps"},
		{Text: "list", Description: "List resources (tasks, pipelines, stepactions)"},
		{Text: "show", Description: "Show details of a resource (task, pipeline)"},
		{Text: "export", Description: "Export resources"},
		{Text: "apply", Description: "Apply resources to Kubernetes cluster"},
		{Text: "undo", Description: "Revert the last action"},
		{Text: "reset", Description: "Reset the current session"},
		{Text: "exit", Description: "Exit the shell"},
		{Text: "quit", Description: "Exit the shell"},

		// Actions (could be context-dependent)
		{Text: "create", Description: "Create a new resource"},
		{Text: "add", Description: "Add to an existing resource"},
		{Text: "select", Description: "Select an existing resource as current context"},
		{Text: "all", Description: "Target all applicable items (e.g., for export or apply)"},
		// Common arguments for list
		{Text: "tasks", Description: "Target tasks (e.g., list tasks)"},
		{Text: "pipelines", Description: "Target pipelines (e.g., list pipelines)"},
		{Text: "stepactions", Description: "Target stepactions (e.g., list stepactions)"},
	}

	return prompt.FilterHasPrefix(s, d.GetWordBeforeCursor(), true)
}

// Run starts the interactive REPL.
func Run() {
	sess = state.NewSession()
	p := prompt.New(
		executor,
		completer,
		prompt.OptionTitle("tkn-shell"),
		prompt.OptionPrefix(livePrefix),
		prompt.OptionLivePrefix(func() (string, bool) { return livePrefix, true }),
		prompt.OptionSuggestionBGColor(prompt.DarkGray),
		prompt.OptionSuggestionTextColor(prompt.White),
		prompt.OptionDescriptionBGColor(prompt.DarkGray),
		prompt.OptionDescriptionTextColor(prompt.White),
		prompt.OptionSelectedSuggestionBGColor(prompt.LightGray),
		prompt.OptionSelectedSuggestionTextColor(prompt.Black),
		prompt.OptionSelectedDescriptionBGColor(prompt.LightGray),
		prompt.OptionSelectedDescriptionTextColor(prompt.Black),
		prompt.OptionMaxSuggestion(15),
	)
	feedback.Infof("Welcome to tkn-shell. Type 'help' for assistance or 'exit'/'quit' to quit.")
	p.Run()
}
