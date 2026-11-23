package ports

import (
	"os"
	"strconv"
	"strings"
)

// kernelConnections counts the established connections to port in the kernel's tables.
func kernelConnections(port int) (int, bool) {
	count, read := 0, false
	hexPort := strings.ToUpper(strconv.FormatInt(int64(port), 16))
	for _, table := range procNetTCP {
		data, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		read = true
		for _, line := range strings.Split(string(data), "\n")[1:] {
			f := strings.Fields(line)
			// 01 is ESTABLISHED.
			if len(f) < 4 || f[3] != "01" {
				continue
			}
			if i := strings.LastIndex(f[1], ":"); i >= 0 && strings.TrimLeft(f[1][i+1:], "0") == hexPort {
				count++
			}
		}
	}
	return count, read
}
