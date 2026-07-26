package codexbridge

import (
	"log"
	"strings"
)

func ConfigureLogging(level string) {
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	if strings.ToUpper(level) == "DEBUG" {
		log.SetPrefix("DEBUG ")
	}
}
