package main

import "log"

// Logger is a simple logger implementing health.Logger interface.
type Logger struct{}

func (l *Logger) Infof(format string, args ...interface{}) {
	log.Printf(format, args...)
}

func (l *Logger) Errorf(format string, args ...interface{}) {
	log.Printf(format, args...)
}
