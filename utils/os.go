package utils

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

func getProcess() int {
	return os.Getpid()
}

func getGoroutineId() string {
	buf := make([]byte, 128)
	buf = buf[:runtime.Stack(buf, false)]
	stackInfo := string(buf)
	return strings.TrimSpace(strings.Split(strings.Split(stackInfo, "[running]")[0], "goroutine")[1])
}

func GetProcessAndGoroutineIDStr() string {
	return fmt.Sprintf("%d_%s", getProcess(), getGoroutineId())
}
