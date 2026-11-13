package cli

import "errors"

// exitDeclined is the exit code of a command whose administrator permission prompt was declined:
// the apps tell it from a failure (1) without matching the message.
const exitDeclined = 4

// errDeclined is returned when the password or permission prompt was cancelled.
var errDeclined = errors.New("administrator permission was declined")

// ExitCode is the status flock exits with after err.
func ExitCode(err error) int {
	if errors.Is(err, errDeclined) {
		return exitDeclined
	}
	return 1
}
