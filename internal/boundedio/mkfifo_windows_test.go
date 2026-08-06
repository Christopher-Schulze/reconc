//go:build windows

package boundedio

import "errors"

func syscallMkfifo(string) error {
	return errors.New("FIFO is unavailable on Windows")
}
