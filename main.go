package main

import (
	"fmt"
	"os"

	"github.com/c-bata/go-prompt"
)

func completer(d prompt.Document) []prompt.Suggest {
	s := []prompt.Suggest{
		{Text: "exit", Description: "Exit the shell"},
	}
	return prompt.FilterHasPrefix(s, d.GetWordBeforeCursor(), true)
}

func executor(in string) {
	if in == "exit" {
		fmt.Println("Bye!")
		os.Exit(0)
		return
	}
	fmt.Println("You selected " + in)
}

func main() {
	p := prompt.New(
		executor,
		completer,
		prompt.OptionPrefix("tkn > "),
		prompt.OptionTitle("tkn-shell"),
	)
	p.Run()
}
