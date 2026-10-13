package desktop

import (
	"strconv"
	"strings"
)

func automaticGroupName(name string) bool {
	number, ok := strings.CutPrefix(name, "Grupo ")
	if !ok {
		return false
	}
	value, err := strconv.ParseUint(number, 10, 64)
	return err == nil && value > 0
}
