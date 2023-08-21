package main

import (
	"fmt"
	"os"
	"parser/parse"
)

func main() {
	f, err := os.OpenFile("temp", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if len(os.Args) != 2 {
		fmt.Println("usage: parser [file|directory]")
		return
	}

	g, err := parse.Parse(os.Args[1])
	if err != nil {
		panic(err)
	}

	// add yourself output format
	for _, v := range g.Functions {
		fmt.Println("------------------------------------------------------------------")
		fmt.Println(v.Name)
		v.TableUsed.Walk(func(s string) bool {
			fmt.Println(s)
			return true
		})
	
		fmt.Println("-------------")
	
		v.Invoked.Walk(func(s string) bool {
			fmt.Println(s)
			return true
		})
	}
}
