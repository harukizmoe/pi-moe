package agent

import "fmt"

func safeToolErrorContent(toolName string) string {
	return fmt.Sprintf("tool %q failed", toolName)
}
