package main

import (
	"io/ioutil"
	"log"
	"os"
)

const (
	LogLevelError   = "ERROR"
	LogLevelWarning = "WARN"
	LogLevelInfo    = "INFO"
	LogLevelDebug   = "DEBUG"
)

var (
	Debug   *log.Logger = log.New(ioutil.Discard, "[DEBUG] ", log.Ldate|log.Ltime|log.Lshortfile)
	Info    *log.Logger = log.New(ioutil.Discard, "[INFO] ", log.Ldate|log.Ltime|log.Lshortfile)
	Warning *log.Logger = log.New(ioutil.Discard, "[WARN] ", log.Ldate|log.Ltime|log.Lshortfile)
	Error   *log.Logger = log.New(ioutil.Discard, "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile)
)

func InitLogging(logLevel string) {
	switch logLevel {
	case LogLevelDebug:
		Debug.SetOutput(os.Stdout)
		fallthrough
	case LogLevelInfo:
		Info.SetOutput(os.Stdout)
		fallthrough
	case LogLevelWarning:
		Warning.SetOutput(os.Stdout)
		fallthrough
	case LogLevelError:
		Error.SetOutput(os.Stderr)
	}
}
