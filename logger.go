package pipeline

import (
	"fmt"
	"log"
	"os"
)

type LogLevel int

const (
	TraceLevel LogLevel = iota
	DebugLevel
	InfoLevel
	ErrorLevel
)

var (
	logger = log.New(os.Stdout, "", log.LstdFlags)
	level  = InfoLevel
)

func SetLevel(l LogLevel) {
	level = l
}

func Trace(args ...interface{}) {
	if level <= TraceLevel {
		logger.Printf("[TRACE] %s", fmt.Sprint(args...))
	}
}

func Tracef(format string, args ...interface{}) {
	if level <= TraceLevel {
		logger.Printf("[TRACE] %s", fmt.Sprintf(format, args...))
	}
}

func Debug(args ...interface{}) {
	if level <= DebugLevel {
		logger.Printf("[DEBUG] %s", fmt.Sprint(args...))
	}
}

func Debugf(format string, args ...interface{}) {
	if level <= DebugLevel {
		logger.Printf("[DEBUG] %s", fmt.Sprintf(format, args...))
	}
}

func Info(args ...interface{}) {
	if level <= InfoLevel {
		logger.Printf("[INFO] %s", fmt.Sprint(args...))
	}
}

func Infof(format string, args ...interface{}) {
	if level <= InfoLevel {
		logger.Printf("[INFO] %s", fmt.Sprintf(format, args...))
	}
}

func Error(args ...interface{}) {
	if level <= ErrorLevel {
		logger.Printf("[ERROR] %s", fmt.Sprint(args...))
	}
}

func Errorf(format string, args ...interface{}) {
	if level <= ErrorLevel {
		logger.Printf("[ERROR] %s", fmt.Sprintf(format, args...))
	}
}
