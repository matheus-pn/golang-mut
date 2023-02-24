package kekw

import (
	"fmt"
	"log"
	"os"
)

func Function() {
	var err error
	if err != nil {
		log.Fatalf(err.Error())
	}

	if len(os.Environ()) > 0 {
		fmt.Println("lalala")
	}
}
