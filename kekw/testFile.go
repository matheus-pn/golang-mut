package kekw

import "fmt"

func HasMutation() {
	a := 0
	fmt.Println(a)
}

func NoMutation() string {
	return "joiqeoiqwje"
}

func If() {
	if true {
		a := 0
		fmt.Println(a)
	} else {
		fmt.Println("jiorwjeoijqwe")
	}
}

func Switch(n int) {
	switch n {
	case 0:
	default:
	}
}

func Switch2(n string) {
	switch n {
	case "qiwe":
	default:
		fmt.Println(0)
	}
}

func Switch3(n string) {
	switch n {
	case "qiwe":
	default:
		if true {
			fmt.Println(0)
		}
	}
}

func TypeSwitch1(n interface{}) {
	switch n.(type) {
	case int:
		fmt.Println(0)
	default:
	}
}

func TypeSwitch2(n interface{}) {
	switch n.(type) {
	case int:
	default:
	}
}

func Select1(a <-chan struct{}, b <-chan struct{}) {
	select {
	case <-a:
		fmt.Println(0)
	case <-b:
	}
}

func Select2(a <-chan struct{}, b <-chan struct{}) {
	select {
	case <-a:
	case <-b:
	}
}
