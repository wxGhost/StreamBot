package bot

import (
	"fmt"
	"strconv"
	"strings"
)

// cbData encodes a callback action + proposal ID into a compact string.
// Format: "action:id"  e.g. "like:42"
func cbData(action string, proposalID int) string {
	return fmt.Sprintf("%s:%d", action, proposalID)
}

// parseCB decodes a callback data string back to action + id.
func parseCB(data string) (action string, id int, err error) {
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid callback data: %q", data)
	}
	action = parts[0]
	id, err = strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid id in callback: %w", err)
	}
	return action, id, nil
}
