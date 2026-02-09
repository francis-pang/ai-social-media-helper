package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
)

// PromptForDirectory prompts the user interactively for a directory path.
// Returns the current directory if the user enters nothing.
func PromptForDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	fmt.Printf("Directory [%s]: ", cwd)

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read input, using current directory")
		return cwd
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return cwd
	}

	return input
}
