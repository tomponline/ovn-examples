package main

import (
	"fmt"

	goovn "github.com/ebay/go-ovn"
)

func main() {
	ovndbapi, err := goovn.NewClient(&goovn.Config{Addr: "tcp:127.0.0.1:6643"})
	if err != nil {
		panic(err)
	}

	lports, err := ovndbapi.LSPList("project1-net2-ls-int")
	if err != nil {
		panic(err)
	}

	for _, lp := range lports {
		fmt.Printf("%v\n", *lp)
	}
}
