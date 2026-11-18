// xymmigrate imports an offline SQLite backup into an empty MySQL database.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"turnrpg/internal/store"
)

func main() {
	source := flag.String("source", "", "SQLite backup file (read only)")
	check := flag.Bool("check", false, "check source and print row counts without connecting to MySQL")
	flag.Parse()
	if *source == "" {
		fmt.Fprintln(os.Stderr, "-source is required")
		os.Exit(2)
	}
	report, err := store.CheckSQLiteSource(*source)
	if err == nil && !*check {
		var target *store.MySQL
		target, err = store.OpenMySQL("")
		if err == nil {
			report, err = target.ImportSQLite(*source)
			target.Close()
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
